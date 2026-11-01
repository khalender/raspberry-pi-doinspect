// Package derive turns readings of a machine into the series doinspect keeps: percentages and
// sizes as they are, counters as rates per second between two readings.
package derive

import (
	"math"
	"sort"

	"doinspect/server/internal/raw"
)

// Sample is one point of one series of one machine, for example "cpu", "ram.used" or "net[eth0].rx".
type Sample struct {
	Name  string
	Value float64
}

// Machine remembers a machine's previous reading.
type Machine struct{ prev *raw.Raw }

// Next returns the samples for reading cur. Gauges come from cur alone; rates need the previous
// reading, so the first reading after a start or a gap of more than gap seconds gives none. A
// counter that went down (a restart of the machine or the exporter) gives no rate this time.
func (m *Machine) Next(cur *raw.Raw, gap float64) []Sample {
	var out []Sample
	add := func(name string, v float64) {
		if !math.IsNaN(v) && !math.IsInf(v, 0) {
			out = append(out, Sample{name, v})
		}
	}

	add("ram.total", cur.MemTotal)
	add("ram.used", cur.MemTotal-cur.MemAvailable)
	add("ram.cached", cur.MemCached)
	if cur.SwapTotal > 0 {
		add("swap.used", cur.SwapTotal-cur.SwapFree)
	}
	add("load1", cur.Load1)
	add("load5", cur.Load5)
	add("load15", cur.Load15)
	for _, mnt := range keys(cur.FS) {
		if s := cur.FS[mnt]; s.Size > 0 {
			add("fs["+mnt+"].used", 100*(s.Size-s.Avail)/s.Size)
			add("fs["+mnt+"].size", s.Size)
		}
	}
	add("uptime", float64(cur.Time.Unix())-cur.BootTime)
	add("temp", cur.TempC)
	add("freq", cur.FreqHz/1e6)

	prev := m.prev
	m.prev = cur
	if prev == nil {
		return out
	}
	dt := cur.Time.Sub(prev.Time).Seconds()
	if dt <= 0 || dt > gap {
		return out
	}

	var busy, total float64
	for _, id := range keys(cur.CPU) {
		c := cur.CPU[id]
		p, ok := prev.CPU[id]
		db, dtot := c.Busy-p.Busy, c.Total-p.Total
		if !ok || dtot <= 0 || db < 0 {
			continue
		}
		add("cpu["+id+"]", 100*db/dtot)
		busy, total = busy+db, total+dtot
	}
	if total > 0 {
		add("cpu", 100*busy/total)
	}
	rate := func(name string, c, p float64) {
		if c >= p {
			add(name, (c-p)/dt)
		}
	}
	for _, d := range keys(cur.Disk) {
		if p, ok := prev.Disk[d]; ok {
			rate("disk["+d+"].read", cur.Disk[d].In, p.In)
			rate("disk["+d+"].write", cur.Disk[d].Out, p.Out)
		}
	}
	for _, n := range keys(cur.Net) {
		if p, ok := prev.Net[n]; ok {
			rate("net["+n+"].rx", cur.Net[n].In, p.In)
			rate("net["+n+"].tx", cur.Net[n].Out, p.Out)
		}
	}
	return out
}

func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
