# sup-modbus

`sup-modbus` is a high-reliability Modbus client implementation for Go, built on top of the [sup](https://github.com/webermarci/sup) actor library. It provides a thread-safe, supervised, and observable way to interact with Modbus devices over TCP, RTU, or ASCII.

## Why this exists?

Modbus is a single-threaded protocol by nature. If two different parts of your application try to access the same Serial or TCP port simultaneously, you get collisions, corrupted data, or "resource busy" errors. 

This library solves that by treating the Modbus connection as an **Actor**. All requests are queued in a mailbox and processed sequentially by the actor loop, ensuring hardware access is perfectly serialized.

## Features

* **Actor-Based Concurrency**: Thread-safe access to hardware. Multiple goroutines can call the actor safely; the actor handles the queue.
* **Supervised Lifecycle**: Designed to run under a `sup.Supervisor`. If the connection drops (EOF/Timeout), the actor returns a fatal error, allowing the supervisor to handle reconnection.
* **Protocol Support**: Supports Modbus TCP, RTU (RS485/RS232), and ASCII.
* **Rich Observability**: Built-in `Observer` interface to track function codes, slave IDs, data payloads, and timing/latency.
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
    client := modbus.NewModbusActor(
        modbus.TCP, 
        "192.168.1.50:502", 
        1, // Slave ID
        modbus.WithTimeout(2 * time.Second),
    )

    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    
    supervisor := sup.NewSupervisor(
			sup.WithPolicy(sup.Permanent),
			sup.WithRestartDelay(time.Second),
			sup.WithRestartLimit(5, 10 * time.Second),
		)
		supervisor.Go(ctx, client)

    res, err := client.ReadHoldingRegisters(100, 2)
    if err != nil {
        panic(err)
    }

    fmt.Printf("Register Data: %X\n", res)
}
```
