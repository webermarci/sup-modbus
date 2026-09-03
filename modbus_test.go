package modbus

import (
	"context"
	"errors"
	"fmt"
	"net"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/goburrow/modbus"
	"github.com/webermarci/sup/resource"
)

type mockClient struct {
	modbus.Client
	readCoilsFn           func(address, quantity uint16) ([]byte, error)
	writeSingleCoilFn     func(address, value uint16) ([]byte, error)
	writeSingleRegisterFn func(address, value uint16) ([]byte, error)
}

type recordedCall struct {
	name string
	args []any
}

type recordingClient struct {
	calls chan recordedCall
}

func (c *recordingClient) record(name string, args ...any) ([]byte, error) {
	c.calls <- recordedCall{name: name, args: args}
	return []byte(name), nil
}

func (c *recordingClient) ReadCoils(address, quantity uint16) ([]byte, error) {
	return c.record("ReadCoils", address, quantity)
}

func (c *recordingClient) ReadDiscreteInputs(address, quantity uint16) ([]byte, error) {
	return c.record("ReadDiscreteInputs", address, quantity)
}

func (c *recordingClient) WriteSingleCoil(address, value uint16) ([]byte, error) {
	return c.record("WriteSingleCoil", address, value)
}

func (c *recordingClient) WriteMultipleCoils(address, quantity uint16, value []byte) ([]byte, error) {
	return c.record("WriteMultipleCoils", address, quantity, value)
}

func (c *recordingClient) ReadHoldingRegisters(address, quantity uint16) ([]byte, error) {
	return c.record("ReadHoldingRegisters", address, quantity)
}

func (c *recordingClient) ReadInputRegisters(address, quantity uint16) ([]byte, error) {
	return c.record("ReadInputRegisters", address, quantity)
}

func (c *recordingClient) WriteSingleRegister(address, value uint16) ([]byte, error) {
	return c.record("WriteSingleRegister", address, value)
}

func (c *recordingClient) WriteMultipleRegisters(address, quantity uint16, value []byte) ([]byte, error) {
	return c.record("WriteMultipleRegisters", address, quantity, value)
}

func (c *recordingClient) ReadWriteMultipleRegisters(readAddress, readQuantity, writeAddress, writeQuantity uint16, value []byte) ([]byte, error) {
	return c.record("ReadWriteMultipleRegisters", readAddress, readQuantity, writeAddress, writeQuantity, value)
}

func (c *recordingClient) MaskWriteRegister(address, andMask, orMask uint16) ([]byte, error) {
	return c.record("MaskWriteRegister", address, andMask, orMask)
}

func (c *recordingClient) ReadFIFOQueue(address uint16) ([]byte, error) {
	return c.record("ReadFIFOQueue", address)
}

func (m *mockClient) ReadCoils(address, quantity uint16) ([]byte, error) {
	return m.readCoilsFn(address, quantity)
}

func (m *mockClient) WriteSingleCoil(address, value uint16) ([]byte, error) {
	return m.writeSingleCoilFn(address, value)
}

func (m *mockClient) WriteSingleRegister(address, value uint16) ([]byte, error) {
	return m.writeSingleRegisterFn(address, value)
}

func useMockClient(actor *Actor, client modbus.Client, released chan<- struct{}) {
	id := actor.ID()
	actor.resource = resource.NewActor(
		id,
		func(ctx context.Context) (*connection, error) {
			return &connection{ctx: ctx, client: client}, nil
		},
		func(context.Context, *connection) error {
			if released != nil {
				released <- struct{}{}
			}
			return nil
		},
	)
}

func startActor(t *testing.T, actor *Actor) (context.CancelFunc, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- actor.Run(ctx)
	}()
	return cancel, done
}

func awaitRun(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(time.Second):
		t.Fatal("actor did not stop")
		return nil
	}
}

func TestReadCoils(t *testing.T) {
	expected := []byte{0x01, 0x01}
	client := &mockClient{
		readCoilsFn: func(address, quantity uint16) ([]byte, error) {
			if address != 100 || quantity != 8 {
				t.Fatalf("ReadCoils(%d, %d), want ReadCoils(100, 8)", address, quantity)
			}
			return expected, nil
		},
	}

	var requestCalled atomic.Bool
	var responseCalled atomic.Bool
	var lastFunctionCode byte
	var lastAddress uint16
	actor := NewActor(t.Name(), TCP, "localhost:502", 1,
		WithOnRequest(func(functionCode, _ byte, address, _ uint16) {
			requestCalled.Store(true)
			lastFunctionCode = functionCode
			lastAddress = address
		}),
		WithOnResponse(func(_ byte, _ byte, _ []byte, _ error, _ time.Duration) {
			responseCalled.Store(true)
		}),
	)
	useMockClient(actor, client, nil)

	cancel, done := startActor(t, actor)
	response, err := actor.ReadCoils(t.Context(), 100, 8)
	if err != nil {
		t.Fatalf("ReadCoils() error = %v", err)
	}
	if !reflect.DeepEqual(response, expected) {
		t.Fatalf("ReadCoils() = %v, want %v", response, expected)
	}
	if !requestCalled.Load() || !responseCalled.Load() {
		t.Fatal("request and response callbacks were not both called")
	}
	if lastFunctionCode != modbus.FuncCodeReadCoils || lastAddress != 100 {
		t.Fatalf("callback metadata = (%d, %d), want (%d, 100)", lastFunctionCode, lastAddress, modbus.FuncCodeReadCoils)
	}

	cancel()
	if err := awaitRun(t, done); err != nil {
		t.Fatalf("Run() after cancellation = %v, want nil", err)
	}
}

func TestTCPConnectionLifecycle(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	accepted := make(chan net.Conn, 1)
	acceptErr := make(chan error, 1)
	go func() {
		connection, err := listener.Accept()
		if err != nil {
			acceptErr <- err
			return
		}
		accepted <- connection
	}()

	started := make(chan struct{})
	actor := NewActor(t.Name(), TCP, listener.Addr().String(), 1,
		WithTimeout(time.Second),
		WithOnStart(func(Protocol, string, byte) { close(started) }),
	)
	cancel, done := startActor(t, actor)

	var serverConnection net.Conn
	select {
	case serverConnection = <-accepted:
		t.Cleanup(func() { _ = serverConnection.Close() })
	case err := <-acceptErr:
		t.Fatalf("Accept() error = %v", err)
	case <-time.After(time.Second):
		t.Fatal("actor did not connect")
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("WithOnStart callback was not called")
	}

	cancel()
	if err := awaitRun(t, done); err != nil {
		t.Fatalf("Run() after cancellation = %v, want nil", err)
	}

	if err := serverConnection.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}
	if _, err := serverConnection.Read(make([]byte, 1)); err == nil {
		t.Fatal("server connection remained open after actor stopped")
	} else if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
		t.Fatalf("server connection was not closed before deadline: %v", err)
	}
}

func TestOperationWiring(t *testing.T) {
	client := &recordingClient{calls: make(chan recordedCall, 1)}
	actor := NewActor(t.Name(), TCP, "localhost:502", 1)
	useMockClient(actor, client, nil)
	cancel, done := startActor(t, actor)

	tests := []struct {
		name string
		call func() ([]byte, error)
		args []any
	}{
		{"ReadCoils", func() ([]byte, error) { return actor.ReadCoils(t.Context(), 10, 8) }, []any{uint16(10), uint16(8)}},
		{"ReadDiscreteInputs", func() ([]byte, error) { return actor.ReadDiscreteInputs(t.Context(), 11, 9) }, []any{uint16(11), uint16(9)}},
		{"WriteSingleCoil", func() ([]byte, error) { return actor.WriteSingleCoil(t.Context(), 12, 0xFF00) }, []any{uint16(12), uint16(0xFF00)}},
		{"WriteMultipleCoils", func() ([]byte, error) { return actor.WriteMultipleCoils(t.Context(), 13, 9, []byte{1, 2}) }, []any{uint16(13), uint16(9), []byte{1, 2}}},
		{"ReadHoldingRegisters", func() ([]byte, error) { return actor.ReadHoldingRegisters(t.Context(), 14, 2) }, []any{uint16(14), uint16(2)}},
		{"ReadInputRegisters", func() ([]byte, error) { return actor.ReadInputRegisters(t.Context(), 15, 3) }, []any{uint16(15), uint16(3)}},
		{"WriteSingleRegister", func() ([]byte, error) { return actor.WriteSingleRegister(t.Context(), 16, 42) }, []any{uint16(16), uint16(42)}},
		{"WriteMultipleRegisters", func() ([]byte, error) { return actor.WriteMultipleRegisters(t.Context(), 17, 2, []byte{1, 2, 3, 4}) }, []any{uint16(17), uint16(2), []byte{1, 2, 3, 4}}},
		{"ReadWriteMultipleRegisters", func() ([]byte, error) {
			return actor.ReadWriteMultipleRegisters(t.Context(), 18, 2, 19, 2, []byte{1, 2, 3, 4})
		}, []any{uint16(18), uint16(2), uint16(19), uint16(2), []byte{1, 2, 3, 4}}},
		{"MaskWriteRegister", func() ([]byte, error) { return actor.MaskWriteRegister(t.Context(), 20, 0xFF00, 0x00FF) }, []any{uint16(20), uint16(0xFF00), uint16(0x00FF)}},
		{"ReadFIFOQueue", func() ([]byte, error) { return actor.ReadFIFOQueue(t.Context(), 21) }, []any{uint16(21)}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response, err := test.call()
			if err != nil {
				t.Fatalf("%s() error = %v", test.name, err)
			}
			if string(response) != test.name {
				t.Fatalf("%s() response = %q, want %q", test.name, response, test.name)
			}
			recorded := <-client.calls
			if recorded.name != test.name || !reflect.DeepEqual(recorded.args, test.args) {
				t.Fatalf("recorded call = %#v, want %s%v", recorded, test.name, test.args)
			}
		})
	}

	cancel()
	if err := awaitRun(t, done); err != nil {
		t.Fatalf("Run() after cancellation = %v, want nil", err)
	}
}

func TestModbusExceptionDoesNotStopResource(t *testing.T) {
	protocolErr := &modbus.ModbusError{ExceptionCode: modbus.ExceptionCodeIllegalDataAddress}
	var calls atomic.Int32
	client := &mockClient{
		readCoilsFn: func(uint16, uint16) ([]byte, error) {
			if calls.Add(1) == 1 {
				return nil, protocolErr
			}
			return []byte{0x01}, nil
		},
	}
	actor := NewActor(t.Name(), TCP, "localhost:502", 1)
	useMockClient(actor, client, nil)

	cancel, done := startActor(t, actor)
	if _, err := actor.ReadCoils(t.Context(), 0, 1); !errors.Is(err, protocolErr) {
		t.Fatalf("first ReadCoils() error = %v, want %v", err, protocolErr)
	}

	callCtx, cancelCall := context.WithTimeout(t.Context(), time.Second)
	defer cancelCall()
	response, err := actor.ReadCoils(callCtx, 0, 1)
	if err != nil || !reflect.DeepEqual(response, []byte{0x01}) {
		t.Fatalf("second ReadCoils() = (%v, %v), want ([1], nil)", response, err)
	}

	cancel()
	if err := awaitRun(t, done); err != nil {
		t.Fatalf("Run() after cancellation = %v, want nil", err)
	}
}

func TestConnectionErrorStopsAndReleasesResource(t *testing.T) {
	wantErr := errors.New("connection lost")
	client := &mockClient{
		readCoilsFn: func(uint16, uint16) ([]byte, error) {
			return nil, wantErr
		},
	}
	released := make(chan struct{}, 1)
	actor := NewActor(t.Name(), TCP, "localhost:502", 1)
	useMockClient(actor, client, released)

	_, done := startActor(t, actor)
	if _, err := actor.ReadCoils(t.Context(), 0, 1); !errors.Is(err, wantErr) {
		t.Fatalf("ReadCoils() error = %v, want %v", err, wantErr)
	}
	if err := awaitRun(t, done); !errors.Is(err, wantErr) {
		t.Fatalf("Run() = %v, want %v", err, wantErr)
	}
	select {
	case <-released:
	case <-time.After(time.Second):
		t.Fatal("resource was not released")
	}
}

func TestCallHonorsCanceledContextBeforeRun(t *testing.T) {
	actor := NewActor(t.Name(), TCP, "localhost:502", 1)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := actor.ReadCoils(ctx, 0, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("ReadCoils() error = %v, want context.Canceled", err)
	}
}

func TestInvalidArgumentsDoNotWaitForResource(t *testing.T) {
	actor := NewActor(t.Name(), TCP, "localhost:502", 1)
	tests := []struct {
		name string
		call func() error
	}{
		{"read coils quantity", func() error { _, err := actor.ReadCoils(t.Context(), 0, 0); return err }},
		{"read discrete inputs quantity", func() error { _, err := actor.ReadDiscreteInputs(t.Context(), 0, 2001); return err }},
		{"single coil value", func() error { _, err := actor.WriteSingleCoil(t.Context(), 0, 1); return err }},
		{"multiple coils quantity", func() error { _, err := actor.WriteMultipleCoils(t.Context(), 0, 1969, nil); return err }},
		{"multiple coils values", func() error { _, err := actor.WriteMultipleCoils(t.Context(), 0, 9, []byte{1}); return err }},
		{"holding register quantity", func() error { _, err := actor.ReadHoldingRegisters(t.Context(), 0, 126); return err }},
		{"input register quantity", func() error { _, err := actor.ReadInputRegisters(t.Context(), 0, 0); return err }},
		{"multiple register quantity", func() error { _, err := actor.WriteMultipleRegisters(t.Context(), 0, 124, nil); return err }},
		{"multiple register values", func() error { _, err := actor.WriteMultipleRegisters(t.Context(), 0, 2, []byte{1, 2}); return err }},
		{"read-write read quantity", func() error {
			_, err := actor.ReadWriteMultipleRegisters(t.Context(), 0, 0, 0, 1, []byte{1, 2})
			return err
		}},
		{"read-write write quantity", func() error { _, err := actor.ReadWriteMultipleRegisters(t.Context(), 0, 1, 0, 122, nil); return err }},
		{"read-write values", func() error {
			_, err := actor.ReadWriteMultipleRegisters(t.Context(), 0, 1, 0, 2, []byte{1, 2})
			return err
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.call(); !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("error = %v, want ErrInvalidArgument", err)
			}
		})
	}
}

func TestTransportOptions(t *testing.T) {
	actor := NewActor(t.Name(), RTU, "/dev/ttyUSB0", 7,
		WithTimeout(2*time.Second),
		WithSerialConfig(19200, 7, 2, "N"),
		WithRS485(time.Millisecond, 2*time.Millisecond),
		WithQuietTime(3*time.Millisecond),
	)

	want := actorConfig{
		protocol:             RTU,
		address:              "/dev/ttyUSB0",
		slaveID:              7,
		timeout:              2 * time.Second,
		baudRate:             19200,
		dataBits:             7,
		stopBits:             2,
		parity:               "N",
		rs485Enabled:         true,
		rs485DelayBeforeSend: time.Millisecond,
		rs485DelayAfterSend:  2 * time.Millisecond,
		quietTime:            3 * time.Millisecond,
	}
	if !reflect.DeepEqual(actor.config, want) {
		t.Fatalf("config = %#v, want %#v", actor.config, want)
	}
}

func TestSingleWriteCallbacksReportQuantityOne(t *testing.T) {
	client := &mockClient{
		writeSingleCoilFn:     func(uint16, uint16) ([]byte, error) { return nil, nil },
		writeSingleRegisterFn: func(uint16, uint16) ([]byte, error) { return nil, nil },
	}
	quantities := make(chan uint16, 2)
	actor := NewActor(t.Name(), TCP, "localhost:502", 1,
		WithOnRequest(func(_ byte, _ byte, _ uint16, quantity uint16) {
			quantities <- quantity
		}),
	)
	useMockClient(actor, client, nil)

	cancel, done := startActor(t, actor)
	if _, err := actor.WriteSingleCoil(t.Context(), 10, 0xFF00); err != nil {
		t.Fatalf("WriteSingleCoil() error = %v", err)
	}
	if _, err := actor.WriteSingleRegister(t.Context(), 11, 42); err != nil {
		t.Fatalf("WriteSingleRegister() error = %v", err)
	}
	for range 2 {
		if quantity := <-quantities; quantity != 1 {
			t.Fatalf("callback quantity = %d, want 1", quantity)
		}
	}

	cancel()
	if err := awaitRun(t, done); err != nil {
		t.Fatalf("Run() after cancellation = %v, want nil", err)
	}
}

func TestCallsAreSerialized(t *testing.T) {
	entered := make(chan struct{}, 2)
	releaseCall := make(chan struct{})
	client := &mockClient{
		readCoilsFn: func(uint16, uint16) ([]byte, error) {
			entered <- struct{}{}
			<-releaseCall
			return []byte{1}, nil
		},
	}
	actor := NewActor(t.Name(), TCP, "localhost:502", 1)
	useMockClient(actor, client, nil)
	cancel, done := startActor(t, actor)

	callDone := make(chan error, 2)
	go func() {
		_, err := actor.ReadCoils(t.Context(), 0, 1)
		callDone <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("first call did not start")
	}

	go func() {
		_, err := actor.ReadCoils(t.Context(), 1, 1)
		callDone <- err
	}()
	select {
	case <-entered:
		t.Fatal("second call started before first call completed")
	case <-time.After(20 * time.Millisecond):
	}

	close(releaseCall)
	for range 2 {
		select {
		case err := <-callDone:
			if err != nil {
				t.Fatalf("ReadCoils() error = %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("call did not complete")
		}
	}

	cancel()
	if err := awaitRun(t, done); err != nil {
		t.Fatalf("Run() after cancellation = %v, want nil", err)
	}
}

func TestErrorClassification(t *testing.T) {
	if isFatal(nil) {
		t.Fatal("nil error classified as fatal")
	}
	if isFatal(&modbus.ModbusError{ExceptionCode: modbus.ExceptionCodeIllegalDataAddress}) {
		t.Fatal("Modbus exception classified as fatal")
	}
	if isFatal(fmt.Errorf("request failed: %w", &modbus.ModbusError{})) {
		t.Fatal("wrapped Modbus exception classified as fatal")
	}
	if !isFatal(errors.New("EOF")) {
		t.Fatal("I/O error classified as nonfatal")
	}
}
