package raw

import (
	"io"
	"math"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
)

// FromExporter reads a node_exporter page (Prometheus text format) taken at t. It knows the names
// of node_exporter on Linux (the Pi host) and on FreeBSD (OPNsense's plugin).
func FromExporter(page io.Reader, t time.Time) (*Raw, error) {
	p := expfmt.NewTextParser(model.UTF8Validation)
	mfs, err := p.TextToMetricFamilies(page)
	if err != nil {
		return nil, err
	}
	r := New(t)
	each := func(name string, f func(labels map[string]string, v float64)) {
		mf := mfs[name]
		if mf == nil {
			return
		}
		for _, m := range mf.GetMetric() {
			labels := map[string]string{}
			for _, l := range m.GetLabel() {
				labels[l.GetName()] = l.GetValue()
			}
			f(labels, value(m))
		}
	}
	one := func(name string) float64 {
		v := math.NaN()
		each(name, func(_ map[string]string, x float64) { v = x })
		return v
	}

	each("node_cpu_seconds_total", func(l map[string]string, v float64) {
		c := r.CPU[l["cpu"]]
		c.Total += v
		if l["mode"] != "idle" && l["mode"] != "iowait" {
			c.Busy += v
		}
		r.CPU[l["cpu"]] = c
	})

	// Linux reports MemAvailable; FreeBSD reports page queues, where free, inactive and cache
	// pages are the ones a program can get.
	if r.MemTotal = one("node_memory_MemTotal_bytes"); !math.IsNaN(r.MemTotal) {
		r.MemAvailable = one("node_memory_MemAvailable_bytes")
		r.MemCached = sum(one("node_memory_Cached_bytes"), one("node_memory_Buffers_bytes"))
		r.SwapTotal = one("node_memory_SwapTotal_bytes")
		r.SwapFree = one("node_memory_SwapFree_bytes")
	} else if r.MemTotal = one("node_memory_size_bytes"); !math.IsNaN(r.MemTotal) {
		r.MemCached = sum(one("node_memory_inactive_bytes"), one("node_memory_cache_bytes"))
		r.MemAvailable = sum(one("node_memory_free_bytes"), r.MemCached)
		r.SwapTotal = one("node_memory_swap_size_bytes")
		r.SwapFree = r.SwapTotal - one("node_memory_swap_used_bytes")
	}

	r.Load1, r.Load5, r.Load15 = one("node_load1"), one("node_load5"), one("node_load15")

	each("node_disk_read_bytes_total", func(l map[string]string, v float64) {
		if keepDisk(l["device"]) {
			d := r.Disk[l["device"]]
			d.In = v
			r.Disk[l["device"]] = d
		}
	})
	each("node_disk_written_bytes_total", func(l map[string]string, v float64) {
		if keepDisk(l["device"]) {
			d := r.Disk[l["device"]]
			d.Out = v
			r.Disk[l["device"]] = d
		}
	})
	each("node_filesystem_size_bytes", func(l map[string]string, v float64) {
		if keepFS(l["mountpoint"], l["fstype"]) {
			s := r.FS[l["mountpoint"]]
			s.Size = v
			r.FS[l["mountpoint"]] = s
		}
	})
	each("node_filesystem_avail_bytes", func(l map[string]string, v float64) {
		if keepFS(l["mountpoint"], l["fstype"]) {
			s := r.FS[l["mountpoint"]]
			s.Avail = v
			r.FS[l["mountpoint"]] = s
		}
	})
	each("node_network_receive_bytes_total", func(l map[string]string, v float64) {
		if keepNet(l["device"]) {
			n := r.Net[l["device"]]
			n.In = v
			r.Net[l["device"]] = n
		}
	})
	each("node_network_transmit_bytes_total", func(l map[string]string, v float64) {
		if keepNet(l["device"]) {
			n := r.Net[l["device"]]
			n.Out = v
			r.Net[l["device"]] = n
		}
	})

	r.BootTime = one("node_boot_time_seconds")
	for _, name := range []string{"node_thermal_zone_temp", "node_hwmon_temp_celsius", "node_cpu_temperature_celsius"} {
		each(name, func(_ map[string]string, v float64) {
			if math.IsNaN(r.TempC) || v > r.TempC {
				r.TempC = v
			}
		})
		if !math.IsNaN(r.TempC) {
			break
		}
	}
	var hz, n float64
	each("node_cpu_scaling_frequency_hertz", func(_ map[string]string, v float64) { hz, n = hz+v, n+1 })
	if n > 0 {
		r.FreqHz = hz / n
	}
	return r, nil
}

func value(m *dto.Metric) float64 {
	switch {
	case m.Counter != nil:
		return m.Counter.GetValue()
	case m.Gauge != nil:
		return m.Gauge.GetValue()
	case m.Untyped != nil:
		return m.Untyped.GetValue()
	}
	return math.NaN()
}

// sum adds the values that are present; NaN when none is.
func sum(vs ...float64) float64 {
	out, any := 0.0, false
	for _, v := range vs {
		if !math.IsNaN(v) {
			out, any = out+v, true
		}
	}
	if !any {
		return math.NaN()
	}
	return out
}
