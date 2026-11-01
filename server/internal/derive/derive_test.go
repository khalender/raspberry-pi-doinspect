package derive

import (
	"math"
	"testing"
	"time"

	"doinspect/server/internal/raw"
)

func reading(sec int, busy, total, rx float64) *raw.Raw {
	r := raw.New(time.Unix(int64(1761998400+sec), 0))
	r.CPU["0"] = raw.CPUTimes{Busy: busy, Total: total}
	r.Net["eth0"] = raw.IO{In: rx, Out: rx / 2}
	r.MemTotal, r.MemAvailable = 1000, 400
	return r
}

func find(ss []Sample, name string) (float64, bool) {
	for _, s := range ss {
		if s.Name == name {
			return s.Value, true
		}
	}
	return math.NaN(), false
}

// TestRates: the first reading gives gauges only; the second gives CPU percent and bytes per
// second; a counter that goes down gives no rate; a gap longer than three intervals gives none.
func TestRates(t *testing.T) {
	var m Machine
	first := m.Next(reading(0, 100, 1000, 0), 45)
	if v, _ := find(first, "ram.used"); v != 600 {
		t.Fatalf("ram.used %v", v)
	}
	if _, ok := find(first, "cpu"); ok {
		t.Fatal("cpu rate from one reading")
	}

	second := m.Next(reading(15, 103, 1015, 1500), 45)
	if v, _ := find(second, "cpu"); v != 20 {
		t.Fatalf("cpu %v, want 20 (3 of 15 s busy)", v)
	}
	if v, _ := find(second, "cpu[0]"); v != 20 {
		t.Fatalf("cpu[0] %v", v)
	}
	if v, _ := find(second, "net[eth0].rx"); v != 100 {
		t.Fatalf("rx %v, want 100 B/s", v)
	}

	reset := m.Next(reading(30, 1, 10, 50), 45) // the machine restarted
	if _, ok := find(reset, "cpu"); ok {
		t.Fatal("rate across a counter reset")
	}
	if _, ok := find(reset, "net[eth0].rx"); ok {
		t.Fatal("rx across a counter reset")
	}

	after := m.Next(reading(30+60, 5, 20, 650), 45)
	if _, ok := find(after, "net[eth0].rx"); ok {
		t.Fatal("rate across a 60 s gap")
	}
	again := m.Next(reading(30+75, 8, 35, 2150), 45)
	if v, _ := find(again, "net[eth0].rx"); v != 100 {
		t.Fatalf("rx after the gap %v", v)
	}
}
