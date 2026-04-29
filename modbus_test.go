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

func TestModbusActor_ReadCoils_Success(t *testing.T) {
	ctx := t.Context()

	expectedData := []byte{0x01, 0x01}
	mClient := &mockClient{
		readCoilsFn: func(address, quantity uint16) ([]byte, error) {
			return expectedData, nil
		},
	}

	var requestCalled bool
	var responseCalled bool
	var lastFC byte
	var lastAddr uint16

	actor := NewActor(t.Name(), TCP, "localhost:502", 1,
		WithOnRequest(func(functionCode, slaveId byte, address, quantity uint16) {
			requestCalled = true
			lastFC = functionCode
			lastAddr = address
		}),
		WithOnResponse(func(functionCode, slaveId byte, response []byte, err error, duration time.Duration) {
			responseCalled = true
		}),
	)
	actor.client = mClient

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-actor.mailbox.Receive():
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
	if !requestCalled || !responseCalled {
		t.Error("Observer methods were not called")
	}
	if lastFC != modbus.FuncCodeReadCoils || lastAddr != 100 {
		t.Errorf("Observer recorded wrong metadata: FC=%v, Addr=%v", lastFC, lastAddr)
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
	actor := NewActor(t.Name(), TCP, "127.0.0.1:502", 1, WithMailboxSize(50))
	if actor.config.mailboxSize != 50 {
		t.Errorf("Expected mailbox size 50, got %d", actor.config.mailboxSize)
	}
}
