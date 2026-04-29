package modbus

import (
	"context"
	"time"

	"github.com/goburrow/modbus"
	"github.com/webermarci/sup"
)

type readCoils struct {
	address  uint16
	quantity uint16
}

type readDiscreteInputs struct {
	address  uint16
	quantity uint16
}

type writeSingleCoil struct {
	address uint16
	value   uint16
}

type writeMultipleCoils struct {
	address  uint16
	quantity uint16
	value    []byte
}

type readInputRegisters struct {
	address  uint16
	quantity uint16
}

type readHoldingRegisters struct {
	address  uint16
	quantity uint16
}

type writeSingleRegister struct {
	address uint16
	value   uint16
}

type writeMultipleRegisters struct {
	address  uint16
	quantity uint16
	value    []byte
}

type readWriteMultipleRegisters struct {
	readAddress   uint16
	readQuantity  uint16
	writeAddress  uint16
	writeQuantity uint16
	value         []byte
}

type maskWriteRegister struct {
	address uint16
	andMask uint16
	orMask  uint16
}

type readFIFOQueue struct {
	address uint16
}

// ActorOption defines a function type for configuring the Actor. It allows for flexible configuration of the actor's behavior and settings when creating a new instance.
type ActorOption func(*Actor)

// WithMailboxSize sets the size of the actor's mailbox. A larger mailbox allows for more concurrent requests to be queued, but may increase memory usage. The default mailbox size is 10.
func WithMailboxSize(size int) ActorOption {
	return func(a *Actor) {
		a.config.mailboxSize = size
	}
}

// WithTimeout sets the timeout duration for Modbus requests. This timeout applies to all requests made by the Actor and determines how long it will wait for a response before considering the request failed.
func WithTimeout(timeout time.Duration) ActorOption {
	return func(a *Actor) {
		a.config.timeout = timeout
	}
}

// WithSerialConfig configures serial communication settings for RTU and ASCII protocols. It allows setting the baud rate, data bits, stop bits, and parity. These settings are essential for establishing a proper serial connection with Modbus devices.
func WithSerialConfig(baud int, dataBits int, stopBits int, parity string) ActorOption {
	return func(a *Actor) {
		a.config.baudRate = baud
		a.config.dataBits = dataBits
		a.config.stopBits = stopBits
		a.config.parity = parity
	}
}

// WithRS485Config configures RS485 settings for RTU protocol. If enabled, it allows setting delays before and after sending data to accommodate RS485 transceiver timing requirements.
func WithRS485Config(enabled bool, delayRts time.Duration, delayCustom time.Duration) ActorOption {
	return func(a *Actor) {
		a.config.rs485Enabled = enabled
		a.config.rs485DelayRts = delayRts
		a.config.rs485DelayCustom = delayCustom
	}
}

// WithOnStart allows the caller to provide a callback function that will be invoked when the Actor starts and establishes a connection to the Modbus device. This can be used for logging, metrics, or other side effects related to the actor's startup.
func WithOnStart(handler func(protocol ModbusProtocol, address string, slaveId byte)) ActorOption {
	return func(a *Actor) {
		a.config.onStart = handler
	}
}

// WithOnRequest allows the caller to provide a callback function that will be invoked before a Modbus request is executed. This can be used for logging, metrics collection, or other side effects related to outgoing Modbus requests.
func WithOnRequest(handler func(functionCode byte, slaveId byte, address uint16, quantity uint16)) ActorOption {
	return func(a *Actor) {
		a.config.onRequest = handler
	}
}

// WithOnResponse allows the caller to provide a callback function that will be invoked after a Modbus response is received. This can be used for logging, metrics collection, or other side effects related to incoming Modbus responses, including any errors that may have occurred.
func WithOnResponse(handler func(functionCode byte, slaveId byte, res []byte, err error, duration time.Duration)) ActorOption {
	return func(a *Actor) {
		a.config.onResponse = handler
	}
}

// ModbusProtocol defines the type of Modbus protocol to use (TCP, RTU, ASCII).
type ModbusProtocol int

const (
	TCP ModbusProtocol = iota
	RTU
	ASCII
)

type actorConfig struct {
	mailboxSize int
	protocol    ModbusProtocol
	address     string
	slaveID     byte
	timeout     time.Duration
	// Serial specific
	baudRate int
	dataBits int
	stopBits int
	parity   string
	// RS485 specific
	rs485Enabled     bool
	rs485DelayRts    time.Duration
	rs485DelayCustom time.Duration
	onStart          func(protocol ModbusProtocol, address string, slaveId byte)
	onRequest        func(functionCode byte, slaveId byte, address uint16, quantity uint16)
	onResponse       func(functionCode byte, slaveId byte, res []byte, err error, duration time.Duration)
}

// Actor is an actor that handles Modbus communication using the specified protocol and configuration.
//
// It processes Modbus requests sequentially and can be configured with various options such as mailbox size, timeouts, serial settings, and an optional observer for monitoring requests and responses.
type Actor struct {
	*sup.BaseActor
	mailbox *sup.Mailbox
	config  *actorConfig
	handler modbus.ClientHandler
	client  modbus.Client
}

// NewActor creates a new Actor with the specified protocol, address, slave ID, and optional configuration options.
func NewActor(name string, protocol ModbusProtocol, address string, slaveId byte, opts ...ActorOption) *Actor {
	a := &Actor{
		BaseActor: sup.NewBaseActor(name),
		config: &actorConfig{
			mailboxSize:      10,
			protocol:         protocol,
			address:          address,
			slaveID:          slaveId,
			timeout:          time.Second,
			baudRate:         9600,
			dataBits:         8,
			stopBits:         1,
			parity:           "E",
			rs485Enabled:     false,
			rs485DelayRts:    0,
			rs485DelayCustom: 0,
		},
	}

	for _, opt := range opts {
		opt(a)
	}

	a.mailbox = sup.NewMailbox(a.config.mailboxSize)

	return a
}

// Run starts the Actor and processes incoming requests. It establishes a connection to the Modbus device based on the configured protocol and handles requests sequentially. The actor will continue running until the context is canceled or an unrecoverable error occurs.
func (a *Actor) Run(ctx context.Context) error {
	switch a.config.protocol {
	case TCP:
		h := modbus.NewTCPClientHandler(a.config.address)
		h.SlaveId = a.config.slaveID
		h.Timeout = a.config.timeout

		if err := h.Connect(); err != nil {
			return err
		}
		defer h.Close()
		a.handler = h

	case RTU:
		h := modbus.NewRTUClientHandler(a.config.address)
		h.BaudRate = a.config.baudRate
		h.DataBits = a.config.dataBits
		h.StopBits = a.config.stopBits
		h.Parity = a.config.parity
		h.SlaveId = a.config.slaveID
		h.Timeout = a.config.timeout
		h.RS485.Enabled = a.config.rs485Enabled
		h.RS485.DelayRtsBeforeSend = a.config.rs485DelayRts
		h.RS485.DelayRtsAfterSend = a.config.rs485DelayRts

		if err := h.Connect(); err != nil {
			return err
		}
		defer h.Close()
		a.handler = h

	case ASCII:
		h := modbus.NewASCIIClientHandler(a.config.address)
		h.BaudRate = a.config.baudRate
		h.DataBits = a.config.dataBits
		h.StopBits = a.config.stopBits
		h.Parity = a.config.parity
		h.SlaveId = a.config.slaveID
		h.Timeout = a.config.timeout

		if err := h.Connect(); err != nil {
			return err
		}
		defer h.Close()
		a.handler = h

	default:
		panic("unsupported protocol")
	}

	a.client = modbus.NewClient(a.handler)

	if a.config.onStart != nil {
		a.config.onStart(
			a.config.protocol,
			a.config.address,
			a.config.slaveID,
		)
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case msg, ok := <-a.mailbox.Receive():
			if !ok {
				return nil
			}

			var fatalErr error

			switch m := msg.(type) {
			case sup.CallRequest[readCoils, []byte]:
				p := m.Payload()
				res, err := a.execute(
					modbus.FuncCodeReadCoils,
					p.address,
					p.quantity,
					func() ([]byte, error) {
						return a.client.ReadCoils(p.address, p.quantity)
					},
				)
				fatalErr = handleFatalErr(m, res, err)

			case sup.CallRequest[readDiscreteInputs, []byte]:
				p := m.Payload()
				res, err := a.execute(
					modbus.FuncCodeReadDiscreteInputs,
					p.address,
					p.quantity,
					func() ([]byte, error) {
						return a.client.ReadDiscreteInputs(p.address, p.quantity)
					},
				)
				fatalErr = handleFatalErr(m, res, err)

			case sup.CallRequest[writeSingleCoil, []byte]:
				p := m.Payload()
				res, err := a.execute(
					modbus.FuncCodeWriteSingleCoil,
					p.address,
					p.value,
					func() ([]byte, error) {
						return a.client.WriteSingleCoil(p.address, p.value)
					},
				)
				fatalErr = handleFatalErr(m, res, err)

			case sup.CallRequest[writeMultipleCoils, []byte]:
				p := m.Payload()
				res, err := a.execute(
					modbus.FuncCodeWriteMultipleCoils,
					p.address,
					p.quantity,
					func() ([]byte, error) {
						return a.client.WriteMultipleCoils(p.address, p.quantity, p.value)
					},
				)
				fatalErr = handleFatalErr(m, res, err)

			case sup.CallRequest[readInputRegisters, []byte]:
				p := m.Payload()
				res, err := a.execute(
					modbus.FuncCodeReadInputRegisters,
					p.address,
					p.quantity,
					func() ([]byte, error) {
						return a.client.ReadInputRegisters(p.address, p.quantity)
					},
				)
				fatalErr = handleFatalErr(m, res, err)

			case sup.CallRequest[readHoldingRegisters, []byte]:
				p := m.Payload()
				res, err := a.execute(
					modbus.FuncCodeReadHoldingRegisters,
					p.address,
					p.quantity,
					func() ([]byte, error) {
						return a.client.ReadHoldingRegisters(p.address, p.quantity)
					},
				)
				fatalErr = handleFatalErr(m, res, err)

			case sup.CallRequest[writeSingleRegister, []byte]:
				p := m.Payload()
				res, err := a.execute(
					modbus.FuncCodeWriteSingleRegister,
					p.address,
					p.value,
					func() ([]byte, error) {
						return a.client.WriteSingleRegister(p.address, p.value)
					},
				)
				fatalErr = handleFatalErr(m, res, err)

			case sup.CallRequest[writeMultipleRegisters, []byte]:
				p := m.Payload()
				res, err := a.execute(
					modbus.FuncCodeWriteMultipleRegisters,
					p.address,
					p.quantity,
					func() ([]byte, error) {
						return a.client.WriteMultipleRegisters(p.address, p.quantity, p.value)
					},
				)
				fatalErr = handleFatalErr(m, res, err)

			case sup.CallRequest[readWriteMultipleRegisters, []byte]:
				p := m.Payload()
				res, err := a.execute(
					modbus.FuncCodeReadWriteMultipleRegisters,
					p.readAddress,
					p.readQuantity,
					func() ([]byte, error) {
						return a.client.ReadWriteMultipleRegisters(
							p.readAddress,
							p.readQuantity,
							p.writeAddress,
							p.writeQuantity,
							p.value,
						)
					},
				)
				fatalErr = handleFatalErr(m, res, err)

			case sup.CallRequest[maskWriteRegister, []byte]:
				p := m.Payload()
				res, err := a.execute(
					modbus.FuncCodeMaskWriteRegister,
					p.address,
					0,
					func() ([]byte, error) {
						return a.client.MaskWriteRegister(p.address, p.andMask, p.orMask)
					},
				)
				fatalErr = handleFatalErr(m, res, err)

			case sup.CallRequest[readFIFOQueue, []byte]:
				p := m.Payload()
				res, err := a.execute(
					modbus.FuncCodeReadFIFOQueue,
					p.address,
					0,
					func() ([]byte, error) {
						return a.client.ReadFIFOQueue(p.address)
					},
				)
				fatalErr = handleFatalErr(m, res, err)
			}

			if fatalErr != nil {
				return fatalErr
			}

			if a.config.protocol != TCP && a.config.rs485DelayCustom > 0 {
				select {
				case <-time.After(a.config.rs485DelayCustom):
				case <-ctx.Done():
					return nil
				}
			}
		}
	}
}

func (a *Actor) execute(
	fc byte,
	address uint16,
	quantity uint16,
	fn func() ([]byte, error),
) ([]byte, error) {
	start := time.Now()

	if a.config.onRequest != nil {
		a.config.onRequest(fc, a.config.slaveID, address, quantity)
	}

	res, err := fn()
	duration := time.Since(start)

	if a.config.onResponse != nil {
		a.config.onResponse(fc, a.config.slaveID, res, err, duration)
	}
	return res, err
}

func (a *Actor) ReadCoils(address, quantity uint16) ([]byte, error) {
	return sup.Call[readCoils, []byte](
		a.mailbox,
		readCoils{
			address:  address,
			quantity: quantity,
		},
	)
}

func (a *Actor) ReadDiscreteInputs(address, quantity uint16) ([]byte, error) {
	return sup.Call[readDiscreteInputs, []byte](
		a.mailbox,
		readDiscreteInputs{
			address:  address,
			quantity: quantity,
		},
	)
}

func (a *Actor) WriteSingleCoil(address, value uint16) ([]byte, error) {
	return sup.Call[writeSingleCoil, []byte](
		a.mailbox,
		writeSingleCoil{
			address: address,
			value:   value,
		},
	)
}

func (a *Actor) WriteMultipleCoils(address, quantity uint16, value []byte) ([]byte, error) {
	return sup.Call[writeMultipleCoils, []byte](
		a.mailbox,
		writeMultipleCoils{
			address:  address,
			quantity: quantity,
			value:    value,
		},
	)
}

func (a *Actor) ReadHoldingRegisters(address, quantity uint16) ([]byte, error) {
	return sup.Call[readHoldingRegisters, []byte](
		a.mailbox,
		readHoldingRegisters{
			address:  address,
			quantity: quantity,
		},
	)
}

func (a *Actor) ReadInputRegisters(address, quantity uint16) ([]byte, error) {
	return sup.Call[readInputRegisters, []byte](
		a.mailbox,
		readInputRegisters{
			address:  address,
			quantity: quantity,
		},
	)
}

func (a *Actor) WriteSingleRegister(address, value uint16) ([]byte, error) {
	return sup.Call[writeSingleRegister, []byte](
		a.mailbox,
		writeSingleRegister{
			address: address,
			value:   value,
		},
	)
}

func (a *Actor) WriteMultipleRegisters(address, quantity uint16, value []byte) ([]byte, error) {
	return sup.Call[writeMultipleRegisters, []byte](
		a.mailbox,
		writeMultipleRegisters{
			address:  address,
			quantity: quantity,
			value:    value,
		},
	)
}

func (a *Actor) ReadWriteMultipleRegisters(readAddress, readQuantity, writeAddress, writeQuantity uint16, value []byte) ([]byte, error) {
	return sup.Call[readWriteMultipleRegisters, []byte](
		a.mailbox,
		readWriteMultipleRegisters{
			readAddress:   readAddress,
			readQuantity:  readQuantity,
			writeAddress:  writeAddress,
			writeQuantity: writeQuantity,
			value:         value,
		},
	)
}

func (a *Actor) MaskWriteRegister(address, andMask, orMask uint16) ([]byte, error) {
	return sup.Call[maskWriteRegister, []byte](
		a.mailbox,
		maskWriteRegister{
			address: address,
			andMask: andMask,
			orMask:  orMask,
		},
	)
}

func (a *Actor) ReadFIFOQueue(address uint16) ([]byte, error) {
	return sup.Call[readFIFOQueue, []byte](
		a.mailbox,
		readFIFOQueue{
			address: address,
		},
	)
}

func isFatal(err error) bool {
	if err == nil {
		return false
	}

	if _, ok := err.(*modbus.ModbusError); ok {
		return false
	}

	return true
}

func handleFatalErr(m sup.RepliableRequest[[]byte], res []byte, err error) error {
	if err != nil {
		m.Reply(nil, err)
		if isFatal(err) {
			return err
		}
		return nil
	}
	m.Reply(res, nil)
	return nil
}
