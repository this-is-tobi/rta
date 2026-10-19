package sys

import (
	"context"

	"golang.org/x/sys/unix"
)

// readMemoryPressure asks the kernel for its memory-pressure level: one sysctl,
// no process, no file.
func readMemoryPressure(context.Context) (pressureLevel, error) {
	level, err := unix.SysctlUint32("kern.memorystatus_vm_pressure_level")
	if err != nil {
		return 0, err
	}
	return pressureOfKernelLevel(level)
}
