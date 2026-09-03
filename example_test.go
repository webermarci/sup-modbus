package modbus_test

import (
	"context"
	"time"

	"github.com/webermarci/sup"
	"github.com/webermarci/sup-modbus"
)

func ExampleActor_ReadHoldingRegisters() {
	device := modbus.NewActor(
		"plc",
		modbus.TCP,
		"192.168.1.50:502",
		1,
		modbus.WithTimeout(2*time.Second),
	)
	_ = sup.NewSupervisor("root", sup.Transient).AddActors(device)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, _ = device.ReadHoldingRegisters(ctx, 100, 2)
}
