package modbus

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/goburrow/modbus"
	"github.com/webermarci/sup"
	"github.com/webermarci/sup/resource"
)

// ErrInvalidArgument identifies a request rejected before it reaches the
// Modbus connection.
var ErrInvalidArgument = errors.New("modbus: invalid argument")

// ActorOption configures an Actor before it starts.
type ActorOption func(*actorConfig)

// WithTimeout sets the timeout for Modbus requests.
func WithTimeout(timeout time.Duration) ActorOption {
	if timeout <= 0 {
		panic("modbus: timeout must be positive")
	}
	return func(config *actorConfig) {
		config.timeout = timeout
	}
}

// WithSerialConfig configures serial communication for RTU and ASCII.
func WithSerialConfig(baud, dataBits, stopBits int, parity string) ActorOption {
	if baud <= 0 {
		panic("modbus: baud rate must be positive")
	}
	if dataBits < 5 || dataBits > 8 {
		panic("modbus: data bits must be between 5 and 8")
	}
	if stopBits != 1 && stopBits != 2 {
		panic("modbus: stop bits must be 1 or 2")
	}
	if parity != "N" && parity != "E" && parity != "O" {
		panic("modbus: parity must be N, E, or O")
	}
	return func(config *actorConfig) {
		config.baudRate = baud
		config.dataBits = dataBits
		config.stopBits = stopBits
		config.parity = parity
	}
}

// WithRS485 enables RS485 and configures its RTS delays.
func WithRS485(delayBeforeSend, delayAfterSend time.Duration) ActorOption {
	if delayBeforeSend < 0 || delayAfterSend < 0 {
		panic("modbus: RS485 delays must not be negative")
	}
	return func(config *actorConfig) {
		config.rs485Enabled = true
		config.rs485DelayBeforeSend = delayBeforeSend
		config.rs485DelayAfterSend = delayAfterSend
	}
}

// WithQuietTime sets the delay after each RTU or ASCII request that leaves the
// connection usable.
func WithQuietTime(delay time.Duration) ActorOption {
	if delay < 0 {
		panic("modbus: quiet time must not be negative")
	}
	return func(config *actorConfig) {
		config.quietTime = delay
	}
}

// WithOnStart sets a callback invoked after a connection is established.
func WithOnStart(handler func(protocol Protocol, address string, slaveID byte)) ActorOption {
	return func(config *actorConfig) {
		config.onStart = handler
	}
}

// WithOnRequest sets a callback invoked before a Modbus request is executed.
func WithOnRequest(handler func(functionCode, slaveID byte, address, quantity uint16)) ActorOption {
	return func(config *actorConfig) {
		config.onRequest = handler
	}
}

// WithOnResponse sets a callback invoked after a Modbus request completes.
func WithOnResponse(handler func(functionCode, slaveID byte, response []byte, err error, duration time.Duration)) ActorOption {
	return func(config *actorConfig) {
		config.onResponse = handler
	}
}

// Protocol identifies a supported Modbus transport.
type Protocol uint8

const (
	// TCP uses Modbus TCP over a network connection.
	TCP Protocol = iota
	// RTU uses Modbus RTU over a serial connection.
	RTU
	// ASCII uses Modbus ASCII over a serial connection.
	ASCII
)

type actorConfig struct {
	protocol Protocol
	address  string
	slaveID  byte
	timeout  time.Duration

	baudRate int
	dataBits int
	stopBits int
	parity   string

	rs485Enabled         bool
	rs485DelayBeforeSend time.Duration
	rs485DelayAfterSend  time.Duration
	quietTime            time.Duration
	onStart              func(protocol Protocol, address string, slaveID byte)
	onRequest            func(functionCode, slaveID byte, address, quantity uint16)
	onResponse           func(functionCode, slaveID byte, response []byte, err error, duration time.Duration)
}

type clientHandler interface {
	modbus.ClientHandler
	Connect() error
	Close() error
}

type connection struct {
	ctx     context.Context
	handler clientHandler
	client  modbus.Client
}

type operationResult struct {
	response []byte
	err      error
}

// Actor owns a Modbus connection and serializes all access to it. Connection
// failures stop the current execution so a sup Supervisor can reconnect it.
type Actor struct {
	config   actorConfig
	resource *resource.Actor[*connection]
}

var _ sup.Actor = (*Actor)(nil)

// NewActor creates a supervised Modbus resource actor.
func NewActor(id string, protocol Protocol, address string, slaveID byte, opts ...ActorOption) *Actor {
	if protocol > ASCII {
		panic("modbus: unsupported protocol")
	}
	a := &Actor{
		config: actorConfig{
			protocol: protocol,
			address:  address,
			slaveID:  slaveID,
			timeout:  time.Second,
			baudRate: 9600,
			dataBits: 8,
			stopBits: 1,
			parity:   "E",
		},
	}

	for _, opt := range opts {
		opt(&a.config)
	}

	a.resource = resource.NewActor(id, a.acquire, releaseConnection)
	return a
}

// ID returns the actor identifier.
func (a *Actor) ID() string { return a.resource.ID() }

// Run acquires the connection and serves serialized operations until the
// context is canceled or a connection-level operation fails.
func (a *Actor) Run(ctx context.Context) error { return a.resource.Run(ctx) }

func (a *Actor) acquire(ctx context.Context) (*connection, error) {
	var handler clientHandler

	switch a.config.protocol {
	case TCP:
		h := modbus.NewTCPClientHandler(a.config.address)
		h.SlaveId = a.config.slaveID
		h.Timeout = a.config.timeout
		handler = h

	case RTU:
		h := modbus.NewRTUClientHandler(a.config.address)
		h.BaudRate = a.config.baudRate
		h.DataBits = a.config.dataBits
		h.StopBits = a.config.stopBits
		h.Parity = a.config.parity
		h.SlaveId = a.config.slaveID
		h.Timeout = a.config.timeout
		h.RS485.Enabled = a.config.rs485Enabled
		h.RS485.DelayRtsBeforeSend = a.config.rs485DelayBeforeSend
		h.RS485.DelayRtsAfterSend = a.config.rs485DelayAfterSend
		handler = h

	case ASCII:
		h := modbus.NewASCIIClientHandler(a.config.address)
		h.BaudRate = a.config.baudRate
		h.DataBits = a.config.dataBits
		h.StopBits = a.config.stopBits
		h.Parity = a.config.parity
		h.SlaveId = a.config.slaveID
		h.Timeout = a.config.timeout
		handler = h
	}

	if err := handler.Connect(); err != nil {
		return nil, err
	}

	acquired := false
	defer func() {
		if !acquired {
			_ = handler.Close()
		}
	}()

	if a.config.onStart != nil {
		a.config.onStart(a.config.protocol, a.config.address, a.config.slaveID)
	}

	acquired = true
	return &connection{
		ctx:     ctx,
		handler: handler,
		client:  modbus.NewClient(handler),
	}, nil
}

func releaseConnection(_ context.Context, connection *connection) error {
	return connection.handler.Close()
}

func (a *Actor) call(
	ctx context.Context,
	functionCode byte,
	address uint16,
	quantity uint16,
	operation func(modbus.Client) ([]byte, error),
) ([]byte, error) {
	result, err := a.resource.Call(ctx, func(_ context.Context, connection *connection) (operationResult, error) {
		start := time.Now()
		if a.config.onRequest != nil {
			a.config.onRequest(functionCode, a.config.slaveID, address, quantity)
		}

		response, operationErr := operation(connection.client)
		if a.config.onResponse != nil {
			a.config.onResponse(functionCode, a.config.slaveID, response, operationErr, time.Since(start))
		}

		if isFatal(operationErr) {
			return operationResult{}, operationErr
		}

		if err := a.waitQuietTime(connection.ctx); err != nil {
			return operationResult{}, err
		}

		// A Modbus exception describes a valid response from a healthy
		// connection. Carry it in the result so resource.Actor does not restart.
		return operationResult{response: response, err: operationErr}, nil
	})
	if err != nil {
		return nil, err
	}
	return result.response, result.err
}

func (a *Actor) waitQuietTime(ctx context.Context) error {
	if a.config.protocol == TCP || a.config.quietTime <= 0 {
		return nil
	}

	timer := time.NewTimer(a.config.quietTime)
	defer timer.Stop()

	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func validateQuantity(name string, quantity, maximum uint16) error {
	if quantity < 1 || quantity > maximum {
		return fmt.Errorf("%w: %s %d must be between 1 and %d", ErrInvalidArgument, name, quantity, maximum)
	}
	return nil
}

func validateByteCount(name string, value []byte, expected int) error {
	if len(value) != expected {
		return fmt.Errorf("%w: %s contains %d bytes; want %d", ErrInvalidArgument, name, len(value), expected)
	}
	return nil
}

// ReadCoils reads the state of one or more coils.
func (a *Actor) ReadCoils(ctx context.Context, address, quantity uint16) ([]byte, error) {
	if err := validateQuantity("quantity", quantity, 2000); err != nil {
		return nil, err
	}
	return a.call(ctx, modbus.FuncCodeReadCoils, address, quantity, func(client modbus.Client) ([]byte, error) {
		return client.ReadCoils(address, quantity)
	})
}

// ReadDiscreteInputs reads the state of one or more discrete inputs.
func (a *Actor) ReadDiscreteInputs(ctx context.Context, address, quantity uint16) ([]byte, error) {
	if err := validateQuantity("quantity", quantity, 2000); err != nil {
		return nil, err
	}
	return a.call(ctx, modbus.FuncCodeReadDiscreteInputs, address, quantity, func(client modbus.Client) ([]byte, error) {
		return client.ReadDiscreteInputs(address, quantity)
	})
}

// WriteSingleCoil writes one coil.
func (a *Actor) WriteSingleCoil(ctx context.Context, address, value uint16) ([]byte, error) {
	if value != 0x0000 && value != 0xFF00 {
		return nil, fmt.Errorf("%w: coil value %#04x must be 0x0000 or 0xFF00", ErrInvalidArgument, value)
	}
	return a.call(ctx, modbus.FuncCodeWriteSingleCoil, address, 1, func(client modbus.Client) ([]byte, error) {
		return client.WriteSingleCoil(address, value)
	})
}

// WriteMultipleCoils writes one or more packed coil values.
func (a *Actor) WriteMultipleCoils(ctx context.Context, address, quantity uint16, value []byte) ([]byte, error) {
	if err := validateQuantity("quantity", quantity, 1968); err != nil {
		return nil, err
	}
	if err := validateByteCount("coil values", value, int(quantity+7)/8); err != nil {
		return nil, err
	}
	return a.call(ctx, modbus.FuncCodeWriteMultipleCoils, address, quantity, func(client modbus.Client) ([]byte, error) {
		return client.WriteMultipleCoils(address, quantity, value)
	})
}

// ReadHoldingRegisters reads one or more holding registers.
func (a *Actor) ReadHoldingRegisters(ctx context.Context, address, quantity uint16) ([]byte, error) {
	if err := validateQuantity("quantity", quantity, 125); err != nil {
		return nil, err
	}
	return a.call(ctx, modbus.FuncCodeReadHoldingRegisters, address, quantity, func(client modbus.Client) ([]byte, error) {
		return client.ReadHoldingRegisters(address, quantity)
	})
}

// ReadInputRegisters reads one or more input registers.
func (a *Actor) ReadInputRegisters(ctx context.Context, address, quantity uint16) ([]byte, error) {
	if err := validateQuantity("quantity", quantity, 125); err != nil {
		return nil, err
	}
	return a.call(ctx, modbus.FuncCodeReadInputRegisters, address, quantity, func(client modbus.Client) ([]byte, error) {
		return client.ReadInputRegisters(address, quantity)
	})
}

// WriteSingleRegister writes one holding register.
func (a *Actor) WriteSingleRegister(ctx context.Context, address, value uint16) ([]byte, error) {
	return a.call(ctx, modbus.FuncCodeWriteSingleRegister, address, 1, func(client modbus.Client) ([]byte, error) {
		return client.WriteSingleRegister(address, value)
	})
}

// WriteMultipleRegisters writes one or more holding registers.
func (a *Actor) WriteMultipleRegisters(ctx context.Context, address, quantity uint16, value []byte) ([]byte, error) {
	if err := validateQuantity("quantity", quantity, 123); err != nil {
		return nil, err
	}
	if err := validateByteCount("register values", value, int(quantity)*2); err != nil {
		return nil, err
	}
	return a.call(ctx, modbus.FuncCodeWriteMultipleRegisters, address, quantity, func(client modbus.Client) ([]byte, error) {
		return client.WriteMultipleRegisters(address, quantity, value)
	})
}

// ReadWriteMultipleRegisters writes registers and reads registers in one transaction.
func (a *Actor) ReadWriteMultipleRegisters(
	ctx context.Context,
	readAddress, readQuantity, writeAddress, writeQuantity uint16,
	value []byte,
) ([]byte, error) {
	if err := validateQuantity("read quantity", readQuantity, 125); err != nil {
		return nil, err
	}
	if err := validateQuantity("write quantity", writeQuantity, 121); err != nil {
		return nil, err
	}
	if err := validateByteCount("register values", value, int(writeQuantity)*2); err != nil {
		return nil, err
	}
	return a.call(ctx, modbus.FuncCodeReadWriteMultipleRegisters, readAddress, readQuantity, func(client modbus.Client) ([]byte, error) {
		return client.ReadWriteMultipleRegisters(readAddress, readQuantity, writeAddress, writeQuantity, value)
	})
}

// MaskWriteRegister modifies one holding register using AND and OR masks.
func (a *Actor) MaskWriteRegister(ctx context.Context, address, andMask, orMask uint16) ([]byte, error) {
	return a.call(ctx, modbus.FuncCodeMaskWriteRegister, address, 0, func(client modbus.Client) ([]byte, error) {
		return client.MaskWriteRegister(address, andMask, orMask)
	})
}

// ReadFIFOQueue reads a FIFO queue from its pointer address.
func (a *Actor) ReadFIFOQueue(ctx context.Context, address uint16) ([]byte, error) {
	return a.call(ctx, modbus.FuncCodeReadFIFOQueue, address, 0, func(client modbus.Client) ([]byte, error) {
		return client.ReadFIFOQueue(address)
	})
}

func isFatal(err error) bool {
	if err == nil {
		return false
	}
	var protocolError *modbus.ModbusError
	return !errors.As(err, &protocolError)
}
