// Package modbus provides supervised, serialized access to Modbus TCP, RTU,
// and ASCII devices.
//
// An [Actor] owns one Modbus connection for each execution attempt and runs
// requests against it one at a time. Concurrent callers are safe: the actor's
// resource primitive serializes their operations so a stateful connection is
// never used concurrently.
//
// # Getting started
//
// Create an actor for the device, add it to a sup supervisor, and run the
// supervisor with the application's context:
//
//	device := modbus.NewActor(
//		"plc",
//		modbus.TCP,
//		"192.168.1.50:502",
//		1,
//		modbus.WithTimeout(2*time.Second),
//	)
//
//	supervisor := sup.NewSupervisor("root", sup.Transient).
//		AddActors(device).
//		SetRestartDelay(time.Second)
//
//	go supervisor.Run(ctx)
//
// Give every request a context, preferably with a deadline:
//
//	requestCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
//	defer cancel()
//
//	data, err := device.ReadHoldingRegisters(requestCtx, 100, 2)
//
// # Lifecycle and recovery
//
// Each [Actor.Run] attempt creates and connects a fresh protocol handler.
// When the attempt ends, the handler is closed before a supervisor may start
// the actor again. Canceling the actor context is a clean shutdown and makes
// Run return nil.
//
// A connection-level request failure is returned both to the caller and from
// Run. A transient supervisor can then reacquire a fresh connection according
// to its restart policy. Requests waiting for the actor remain pending across
// acquisition failures and restart delays until their own contexts end.
// Requests already accepted by an execution are never replayed, because a
// write may have taken effect even when its response was lost.
//
// # Calls and cancellation
//
// All read and write methods block until their operation completes, the
// caller's context ends, or the active resource execution stops. Canceling a
// context removes a request that is still waiting for serialized access.
//
// The underlying goburrow/modbus operations do not accept a context, so
// cancellation cannot interrupt transport I/O after it begins. [WithTimeout]
// bounds TCP connection establishment and in-flight transport I/O. Use a
// request deadline as well so callers do not wait indefinitely while the actor
// is unavailable.
//
// # Errors
//
// Invalid quantities, coil values, and write payload sizes are rejected before
// resource handoff. These errors wrap [ErrInvalidArgument] and do not affect
// the connection.
//
// Modbus exception responses are valid protocol responses. They are returned
// to the caller without stopping the actor. Other operation errors indicate
// transport loss or an unusable response and stop the current execution so
// supervision can decide whether to reconnect.
//
// # Serial transports
//
// Use [WithSerialConfig] for RTU and ASCII serial parameters. [WithRS485]
// enables RTU RS485 mode and configures independent RTS delays before and after
// sending. [WithQuietTime] adds a serialized delay after an RTU or ASCII
// request that leaves the connection usable.
//
// # Observability
//
// [WithOnStart] runs after each successful connection, including supervised
// reconnections. [WithOnRequest] and [WithOnResponse] observe serialized
// operations. Callbacks run synchronously in the actor lifecycle and should
// return promptly.
//
// See the README for a complete application example.
package modbus
