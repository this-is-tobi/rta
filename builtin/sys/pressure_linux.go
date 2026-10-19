package sys

import (
	"context"
	"os"
)

// psiMemoryFile is the kernel's pressure-stall file for memory. It is absent on
// a kernel built without CONFIG_PSI or booted with psi=0, and a container sees
// the host's.
const psiMemoryFile = "/proc/pressure/memory"

func readMemoryPressure(context.Context) (pressureLevel, error) {
	raw, err := os.ReadFile(psiMemoryFile)
	if err != nil {
		return 0, err
	}
	return parsePSI(string(raw))
}
