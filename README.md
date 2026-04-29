# sup-modbus

[![Go Reference](https://pkg.go.dev/badge/github.com/webermarci/sup-modbus.svg)](https://pkg.go.dev/github.com/webermarci/sup-modbus)
[![Test](https://github.com/webermarci/sup-modbus/actions/workflows/test.yml/badge.svg)](https://github.com/webermarci/sup-modbus/actions/workflows/test.yml)
[![License](https://img.shields.io/badge/License-MIT-blue.svg)](https://opensource.org/licenses/MIT)

`sup-modbus` is a high-reliability Modbus client implementation for Go, built on top of the [sup](https://github.com/webermarci/sup) actor library. It provides a thread-safe, supervised, and observable way to interact with Modbus devices over TCP, RTU, or ASCII.

## Why this exists?

Modbus is a single-threaded protocol by nature. If two different parts of your application try to access the same Serial or TCP port simultaneously, you get collisions, corrupted data, or "resource busy" errors. 

This library solves that by treating the Modbus connection as an **Actor**. All requests are queued in a mailbox and processed sequentially by the actor loop, ensuring hardware access is perfectly serialized.

## Features

* **Actor-Based Concurrency**: Thread-safe access to hardware. Multiple goroutines can call the actor safely; the actor handles the queue.
* **Supervised Lifecycle**: Designed to run under a `sup.Supervisor`. If the connection drops (EOF/Timeout), the actor returns a fatal error, allowing the supervisor to handle reconnection.
* **Protocol Support**: Supports Modbus TCP, RTU (RS485/RS232), and ASCII.
* **Industrial Timing**: Support for RS485 RTS delays and custom inter-frame "quiet time" requirements.

## Quick start

```go
package main

import (
    "context"
    "fmt"
    "time"

		"github.com/webermarci/sup"
    "github.com/webermarci/sup-modbus"
)

func main() {
	actor := modbus.NewActor("actor",
		modbus.TCP, 
		"192.168.1.50:502", 
		1, // Slave ID
		modbus.WithTimeout(2 * time.Second),
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
    
	supervisor := sup.NewSupervisor("root",
		sup.WithActor(actor),
		sup.WithPolicy(sup.Transient),
		sup.WithRestartDelay(time.Second),
		sup.WithRestartLimit(5, 10 * time.Second),
	)
    
	supervisor.Run(ctx)

	res, err := client.ReadHoldingRegisters(100, 2)
	if err != nil {
		panic(err)
	}

	fmt.Printf("Register Data: %X\n", res)
}
```

## Reactive Bus Integration

`sup-modbus` works seamlessly with the [sup/bus](https://github.com/webermarci/sup/tree/main/bus) package. This allows you to turn Modbus registers into high-level **Signals**, **Mirrors**, and **Triggers**.

### Polling Registers (Signals)
Use `bus.Signal` to poll a register at a specific interval. The signal will notify subscribers only when the value actually changes.

```go
// Create a raw byte signal from the Modbus actor
temperatureRaw := bus.NewSignal("signal", func() ([]byte, error) {
	return actor.ReadHoldingRegisters(100, 1)
}).WithInterval(1 * time.Second)

// Use a Mirror to decode the bytes into a float64
temperature := bus.NewMirror(func() float64 {
	data := temperatureRaw.Read()
	if len(data) < 2 { return 0 }
	return float64(binary.BigEndian.Uint16(data)) / 10.0
})
```

### Writing to Coils (Triggers)

Use `bus.Trigger` to create a write-only command path for your hardware.

```go
// Create a trigger for a fan relay
fanSwitch := bus.NewTrigger("trigger", func(on bool) error {
	val := uint16(0x0000)
	if on { 
		val = 0xFF00 
	}
	_, err := actor.WriteSingleCoil(5, val)
	return err
})

// Writing is now decoupled from Modbus specifics
fanSwitch.Write(true)
```

### Automated Logic

By combining these, you can create reactive control loops that are completely decoupled from the Modbus protocol logic.

```go
// Simple logic using a Mirror
isTooHot := bus.NewMirror(func() bool {
	return temperature.Read() > 28.5
})

// Bridge the signal to the trigger
go func() {
	for range tempRaw.Subscribe(ctx) {
		if isTooHot.Read() {
			fanSwitch.Write(true)
    }
	}
}()
```

## Using with a Supervisor

The `modbus.Actor` implements the `sup.Actor` interface. It should be managed by a supervisor to handle connection drops or hardware timeouts.

```go
supervisor := sup.NewSupervisor("root",
	sup.WithActors(actor, tempSignal), // Signals are also actors!
	sup.WithPolicy(sup.Transient),
	sup.WithRestartDelay(time.Second),
)

supervisor.Run(ctx)
```
