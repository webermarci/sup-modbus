package modbus

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/goburrow/modbus"
	"github.com/webermarci/sup"
)

type mockClient struct {
	modbus.Client
	readCoilsFn func(address, quantity uint16) ([]byte, error)
}

func (m *mockClient) ReadCoils(address, quantity uint16) ([]byte, error) {
	if m.readCoilsFn != nil {
		return m.readCoilsFn(address, quantity)
	}
	return nil, nil
}

type mockObserver struct {
	requestCalled  bool
	responseCalled bool
	lastFC         byte
	lastAddr       uint16
}

func (m *mockObserver) OnRequest(fc byte, slaveId byte, addr uint16, qty uint16) {
	m.requestCalled = true
	m.lastFC = fc
	m.lastAddr = addr
}

func (m *mockObserver) OnResponse(fc byte, slaveId byte, res []byte, err error, dur time.Duration) {
	m.responseCalled = true
}

func TestModbusActor_ReadCoils_Success(t *testing.T) {
	ctx := t.Context()

	expectedData := []byte{0x01, 0x01}
	mClient := &mockClient{
		readCoilsFn: func(address, quantity uint16) ([]byte, error) {
			return expectedData, nil
		},
	}

	obs := &mockObserver{}

	actor := NewModbusActor(TCP, "localhost:502", 1, WithObserver(obs))
	actor.client = mClient

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-actor.Receive():
				if !ok {
					return
				}
				if m, ok := msg.(sup.CallRequest[readCoils, []byte]); ok {
					p := m.Payload()
					res, err := actor.execute(modbus.FuncCodeReadCoils, p.address, p.quantity, func() ([]byte, error) {
						return actor.client.ReadCoils(p.address, p.quantity)
					})
					handleFatalErr(m, res, err)
				}
			}
		}
	}()

	res, err := actor.ReadCoils(100, 8)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
	if !reflect.DeepEqual(res, expectedData) {
		t.Errorf("Expected %v, got %v", expectedData, res)
	}
	if !obs.requestCalled || !obs.responseCalled {
		t.Error("Observer methods were not called")
	}
	if obs.lastFC != modbus.FuncCodeReadCoils || obs.lastAddr != 100 {
		t.Errorf("Observer recorded wrong metadata: FC=%v, Addr=%v", obs.lastFC, obs.lastAddr)
	}
}

func TestModbusActor_ErrorHandling(t *testing.T) {
	t.Run("Non-Fatal Modbus Error", func(t *testing.T) {
		mbErr := &modbus.ModbusError{ExceptionCode: 0x02}

		if isFatal(mbErr) {
			t.Error("Modbus Protocol errors should NOT be fatal")
		}
	})

	t.Run("Fatal IO Error", func(t *testing.T) {
		ioErr := errors.New("EOF")

		if !isFatal(ioErr) {
			t.Error("IO errors (EOF/Timeout) SHOULD be fatal to trigger restart")
		}
	})

	t.Run("Nil is not fatal", func(t *testing.T) {
		if isFatal(nil) {
			t.Error("Nil error should not be fatal")
		}
	})
}

func TestWithMailboxSize(t *testing.T) {
	actor := NewModbusActor(TCP, "127.0.0.1:502", 1, WithMailboxSize(50))
	if actor.config.mailboxSize != 50 {
		t.Errorf("Expected mailbox size 50, got %d", actor.config.mailboxSize)
	}
}
