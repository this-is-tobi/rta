package sys

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// pressureLevel is how short of memory the kernel says the machine is, in the
// three words both kernels' answers fit: normal, warn, critical.
//
// **The kernel's own judgement rather than a percentage of RAM, because used
// memory is not a symptom.** A machine that has filled its memory with cache is
// healthy, and a Mac with 31 of 32 GiB of swap in use and a pressure level of 1
// is healthy too; a threshold on either figure paints those red, and the
// machine that is thrashing at 60% used goes unmarked. Pressure is the number
// that means the machine is waiting on memory, which is the question.
type pressureLevel int

const (
	pressureNormal pressureLevel = iota
	pressureWarn
	pressureCritical
)

// The bands rta grades Linux's `some avg10` by, in percent of the last ten
// seconds. A fixed pair of numbers because the kernel states none: it exports
// the share of time tasks were stalled and leaves the line to the reader. Ten is
// a tenth of the time spent waiting on memory, which a person at the machine
// notices; forty is a machine that is mostly waiting.
const (
	psiWarn     = 10.0
	psiCritical = 40.0
)

// parsePSI reads /proc/pressure/memory. `some` is the share of the time at least
// one task was stalled on memory, and `full` the share in which every task was;
// `some` is the early warning and the one a tile wants, and avg10 is the
// shortest window the kernel keeps, which is the one that moves at a tile's
// pace.
func parsePSI(text string) (pressureLevel, error) {
	for line := range strings.SplitSeq(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != "some" {
			continue
		}
		for _, f := range fields[1:] {
			raw, ok := strings.CutPrefix(f, "avg10=")
			if !ok {
				continue
			}
			avg, err := strconv.ParseFloat(raw, 64)
			if err != nil || math.IsNaN(avg) || math.IsInf(avg, 0) || avg < 0 {
				return 0, fmt.Errorf("avg10 is %q", raw)
			}
			switch {
			case avg >= psiCritical:
				return pressureCritical, nil
			case avg >= psiWarn:
				return pressureWarn, nil
			default:
				return pressureNormal, nil
			}
		}
	}
	return 0, errors.New("no `some avg10` in the pressure file")
}

// pressureOfKernelLevel maps macOS's kern.memorystatus_vm_pressure_level, which
// answers 1 for normal, 2 for warning and 4 for critical, to the same three
// words. Any other value is a kernel this does not know how to read, and is said
// so rather than guessed.
func pressureOfKernelLevel(level uint32) (pressureLevel, error) {
	switch level {
	case 1:
		return pressureNormal, nil
	case 2:
		return pressureWarn, nil
	case 4:
		return pressureCritical, nil
	}
	return 0, fmt.Errorf("the kernel reports memory pressure level %d", level)
}
