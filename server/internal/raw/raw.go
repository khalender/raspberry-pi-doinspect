// Package raw reads one machine's figures at one moment, either from a node_exporter page or from
// the /proc and /sys of the machine doinspect runs on. Counters (CPU seconds, bytes) stay totals;
// package derive turns two readings into rates.
package raw

import (
	"math"
	"time"
)

// Raw is one reading of one machine. A figure the machine does not report is NaN, or absent from
// its map.
type Raw struct {
	Time time.Time

	CPU map[string]CPUTimes // by CPU number

	MemTotal, MemAvailable, MemCached float64 // bytes
	SwapTotal, SwapFree               float64 // bytes

	Load1, Load5, Load15 float64

	Disk map[string]IO    // by device: bytes read and written since boot
	FS   map[string]Space // by mount point
	Net  map[string]IO    // by interface: bytes received and sent since boot

	BootTime float64 // Unix seconds
	TempC    float64 // the hottest sensor
	FreqHz   float64 // average CPU clock
}

// CPUTimes are seconds since boot: Busy is everything except idle and I/O wait.
type CPUTimes struct{ Busy, Total float64 }

// IO is a pair of byte counters: read and written for a disk, received and sent for an interface.
type IO struct{ In, Out float64 }

// Space is a file system's size and the bytes still free for ordinary users.
type Space struct{ Size, Avail float64 }

// New returns a reading with every figure missing.
func New(t time.Time) *Raw {
	nan := math.NaN()
	return &Raw{
		Time: t, CPU: map[string]CPUTimes{}, Disk: map[string]IO{}, FS: map[string]Space{}, Net: map[string]IO{},
		MemTotal: nan, MemAvailable: nan, MemCached: nan, SwapTotal: nan, SwapFree: nan,
		Load1: nan, Load5: nan, Load15: nan, BootTime: nan, TempC: nan, FreqHz: nan,
	}
}
