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

// TestContainerRates: a container's CPU is its share of the whole machine (2 cores here); a
// recreated container, whose counters start again, gives no rate once.
func TestContainerRates(t *testing.T) {
	at := func(sec int, cpu, rx float64) *raw.Raw {
		r := reading(sec, 0, float64(sec)*2, 0)
		r.CPU["1"] = raw.CPUTimes{Total: float64(sec) * 2}
		r.Containers["gitea"] = raw.Container{CPUSec: cpu, RAM: 5e7, Net: raw.IO{In: rx}, HasNet: true}
		return r
	}
	var m Machine
	if ss := m.Next(at(0, 10, 0), 45); !has(ss, "ctr[gitea].ram") || has(ss, "ctr[gitea].cpu") {
		t.Fatalf("first %v", ss)
	}
	ss := m.Next(at(15, 13, 3000), 45)
	if v, _ := find(ss, "ctr[gitea].cpu"); v != 10 {
		t.Fatalf("cpu %v, want 10 (3 s of 30 core-seconds)", v)
	}
	if v, _ := find(ss, "ctr[gitea].rx"); v != 200 {
		t.Fatalf("rx %v", v)
	}
	if ss := m.Next(at(30, 0.2, 10), 45); has(ss, "ctr[gitea].cpu") || has(ss, "ctr[gitea].rx") {
		t.Fatalf("rate across a recreate: %v", ss)
	}
}

func has(ss []Sample, name string) bool { _, ok := find(ss, name); return ok }
