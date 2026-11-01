package raw

import (
	"bufio"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Local reads the machine doinspect runs on, through its /proc and /sys mounted read-only into the
// container (D2). Proc and Sys are those mount points; FS lists the paths whose file system to
// report.
type Local struct {
	Proc, Sys string
	FS        []string
}

// userHZ is the tick length of /proc/stat on every Linux doinspect runs on.
const userHZ = 100

// Read takes one reading at t. A file that cannot be read leaves its figures missing; only a
// missing /proc/stat is an error, because then the mount is wrong.
func (l Local) Read(t time.Time) (*Raw, error) {
	r := New(t)
	if err := l.stat(r); err != nil {
		return nil, err
	}
	l.meminfo(r)
	l.loadavg(r)
	l.diskstats(r)
	l.netdev(r)
	l.thermal(r)
	l.freq(r)
	for _, p := range l.FS {
		if s, ok := statfs(p); ok {
			r.FS[p] = s
		}
	}
	return r, nil
}

func (l Local) lines(name string, f func(fields []string)) error {
	fh, err := os.Open(filepath.Join(l.Proc, name))
	if err != nil {
		return err
	}
	defer fh.Close()
	sc := bufio.NewScanner(fh)
	for sc.Scan() {
		f(strings.Fields(sc.Text()))
	}
	return sc.Err()
}

func num(s string) float64 {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return math.NaN()
	}
	return v
}

// stat reads per-CPU times and the boot time. The first eight columns are user, nice, system,
// idle, iowait, irq, softirq and steal; guest time is already inside user.
func (l Local) stat(r *Raw) error {
	return l.lines("stat", func(f []string) {
		switch {
		case len(f) >= 9 && strings.HasPrefix(f[0], "cpu") && f[0] != "cpu":
			var total float64
			for _, s := range f[1:9] {
				total += num(s)
			}
			idle := num(f[4]) + num(f[5])
			r.CPU[strings.TrimPrefix(f[0], "cpu")] = CPUTimes{Busy: (total - idle) / userHZ, Total: total / userHZ}
		case len(f) == 2 && f[0] == "btime":
			r.BootTime = num(f[1])
		}
	})
}

func (l Local) meminfo(r *Raw) {
	kb := map[string]float64{}
	l.lines("meminfo", func(f []string) {
		if len(f) >= 2 {
			kb[strings.TrimSuffix(f[0], ":")] = num(f[1]) * 1024
		}
	})
	get := func(k string) float64 {
		if v, ok := kb[k]; ok {
			return v
		}
		return math.NaN()
	}
	r.MemTotal, r.MemAvailable = get("MemTotal"), get("MemAvailable")
	r.MemCached = sum(get("Cached"), get("Buffers"))
	r.SwapTotal, r.SwapFree = get("SwapTotal"), get("SwapFree")
}

func (l Local) loadavg(r *Raw) {
	l.lines("loadavg", func(f []string) {
		if len(f) >= 3 {
			r.Load1, r.Load5, r.Load15 = num(f[0]), num(f[1]), num(f[2])
		}
	})
}

// diskstats keeps whole disks: a partition has no folder of its own in /sys/block. Columns 6 and
// 10 are sectors read and written, 512 bytes each.
func (l Local) diskstats(r *Raw) {
	l.lines("diskstats", func(f []string) {
		if len(f) < 10 || !keepDisk(f[2]) {
			return
		}
		if _, err := os.Stat(filepath.Join(l.Sys, "block", f[2])); err != nil {
			return
		}
		r.Disk[f[2]] = IO{In: num(f[5]) * 512, Out: num(f[9]) * 512}
	})
}

// netdev reads PID 1's view: /proc/net follows the reading process's network namespace, which in
// the container is the container's own.
func (l Local) netdev(r *Raw) {
	l.lines("1/net/dev", func(f []string) {
		if len(f) < 10 || !strings.HasSuffix(f[0], ":") {
			return
		}
		dev := strings.TrimSuffix(f[0], ":")
		if keepNet(dev) {
			r.Net[dev] = IO{In: num(f[1]), Out: num(f[9])}
		}
	})
}

func (l Local) thermal(r *Raw) {
	zones, _ := filepath.Glob(filepath.Join(l.Sys, "class/thermal/thermal_zone*/temp"))
	for _, z := range zones {
		b, err := os.ReadFile(z)
		if err != nil {
			continue
		}
		if c := num(strings.TrimSpace(string(b))) / 1000; !math.IsNaN(c) && (math.IsNaN(r.TempC) || c > r.TempC) {
			r.TempC = c
		}
	}
}

func (l Local) freq(r *Raw) {
	files, _ := filepath.Glob(filepath.Join(l.Sys, "devices/system/cpu/cpu*/cpufreq/scaling_cur_freq"))
	var hz, n float64
	for _, f := range files {
		if b, err := os.ReadFile(f); err == nil {
			if v := num(strings.TrimSpace(string(b))); !math.IsNaN(v) {
				hz, n = hz+v*1000, n+1
			}
		}
	}
	if n > 0 {
		r.FreqHz = hz / n
	}
}
