# sup-modbus

[![Go Reference](https://pkg.go.dev/badge/github.com/webermarci/sup-modbus.svg)](https://pkg.go.dev/github.com/webermarci/sup-modbus)
[![Test](https://github.com/webermarci/sup-modbus/actions/workflows/test.yml/badge.svg)](https://github.com/webermarci/sup-modbus/actions/workflows/test.yml)
[![License](https://img.shields.io/badge/License-MIT-blue.svg)](https://opensource.org/licenses/MIT)

`sup-modbus` provides supervised, serialized access to Modbus TCP, RTU, and
ASCII devices. It is built on the [`sup`](https://github.com/webermarci/sup)
resource primitive.

Modbus connections are stateful and must not be used concurrently. An `Actor`
owns one connection per supervised execution and runs calls one at a time.
When a transport operation fails, the actor releases the connection and
returns the error so its supervisor can reconnect it. Modbus protocol
exceptions are returned to the caller without discarding a healthy connection.

## Installation

```sh
go get github.com/webermarci/sup-modbus
```

## Quick start

```go
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/webermarci/sup"
	"github.com/webermarci/sup-modbus"
)

func main() {
	ctx, cancel := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer cancel()

	device := modbus.NewActor(
		"plc",
		modbus.TCP,
		"192.168.1.50:502",
		1,
		modbus.WithTimeout(2*time.Second),
	)

	supervisor := sup.NewSupervisor("root", sup.Transient).
		AddActors(device).
		SetRestartDelay(time.Second).
		SetRestartLimit(5, 10*time.Second)

	done := make(chan error, 1)
	go func() {
		done <- supervisor.Run(ctx)
	}()

	requestCtx, cancelRequest := context.WithTimeout(ctx, 3*time.Second)
	data, err := device.ReadHoldingRegisters(requestCtx, 100, 2)
	cancelRequest()
	if err != nil {
		panic(err)
	}
	fmt.Printf("Register data: %X\n", data)

	cancel()
	if err := <-done; err != nil {
		panic(err)
	}
}
```

Calls wait while the actor is acquiring or reacquiring its connection. Always
give each call a context with an appropriate deadline. A canceled context also
stops a call that is waiting behind another operation. The underlying
`goburrow/modbus` I/O is bounded by `WithTimeout` once it has started.

## Concurrent callers

All methods are safe to call concurrently. The actor serializes them against
the same connection:

```go
coils, err := device.ReadCoils(ctx, 0, 16)
registers, err := device.ReadHoldingRegisters(ctx, 100, 4)
_, err = device.WriteSingleCoil(ctx, 5, 0xFF00)
```

An accepted operation is never replayed after reconnection, which prevents a
write from being applied twice when its outcome is uncertain.

Invalid quantities, coil values, and write payload sizes are rejected before
the request reaches the connection. Use `errors.Is(err,
modbus.ErrInvalidArgument)` to identify these caller errors.

## Serial configuration

RTU and ASCII actors accept serial settings. RTU can additionally configure
RS485 RTS timing and a quiet period between serialized requests:

```go
device := modbus.NewActor(
	"meter",
	modbus.RTU,
	"/dev/ttyUSB0",
	1,
	modbus.WithSerialConfig(19200, 8, 1, "E"),
	modbus.WithRS485(2*time.Millisecond, 2*time.Millisecond),
	modbus.WithQuietTime(5*time.Millisecond),
)
```

## Observability

Use `WithOnStart`, `WithOnRequest`, and `WithOnResponse` for logging and
metrics. Callbacks execute synchronously as part of the actor's serialized
lifecycle and should return promptly.
