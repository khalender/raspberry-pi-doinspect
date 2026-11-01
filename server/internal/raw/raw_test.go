package raw

import (
	"os"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

func readProm(t *testing.T, name string) *Raw {
	t.Helper()
	f, err := os.Open("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	r, err := FromExporter(f, t0)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// TestLinuxExporter reads a Pi host page: CPU busy leaves out idle and iowait, cache counts buffers,
// loop disks, pseudo file systems, lo and VM taps are left out.
func TestLinuxExporter(t *testing.T) {
	r := readProm(t, "linux.prom")
	if c := r.CPU["0"]; c.Busy != 90 || c.Total != 1000 {
		t.Fatalf("cpu0 %+v", c)
	}
	if r.MemTotal != 8e9 || r.MemAvailable != 2.5e9 || r.MemCached != 1e9 || r.SwapTotal != 1e8 {
		t.Fatalf("memory %v %v %v %v", r.MemTotal, r.MemAvailable, r.MemCached, r.SwapTotal)
	}
	if r.Load1 != 0.71 || r.Load5 != 0.64 || r.Load15 != 0.58 {
		t.Fatalf("load %v %v %v", r.Load1, r.Load5, r.Load15)
	}
	if len(r.Disk) != 1 || r.Disk["mmcblk0"] != (IO{1000, 2000}) {
		t.Fatalf("disk %v", r.Disk)
	}
	if len(r.FS) != 1 || r.FS["/"] != (Space{1e11, 2.5e10}) {
		t.Fatalf("fs %v", r.FS)
	}
	if len(r.Net) != 1 || r.Net["br0"] != (IO{5000, 6000}) {
		t.Fatalf("net %v", r.Net)
	}
	if r.TempC != 54.5 || r.FreqHz != 1.65e9 || r.BootTime != 1.7619e9 {
		t.Fatalf("temp %v freq %v boot %v", r.TempC, r.FreqHz, r.BootTime)
	}
}

// TestFreeBSDExporter reads OPNsense's page: memory from page queues, pflog left out.
func TestFreeBSDExporter(t *testing.T) {
	r := readProm(t, "freebsd.prom")
	if r.MemTotal != 1073741824 || r.MemAvailable != 3.5e8 || r.MemCached != 1.5e8 {
		t.Fatalf("memory %v %v %v", r.MemTotal, r.MemAvailable, r.MemCached)
	}
	if c := r.CPU["0"]; c.Busy != 50 || c.Total != 550 {
		t.Fatalf("cpu %+v", c)
	}
	if len(r.Net) != 1 || r.Net["vtnet0"].In != 123 {
		t.Fatalf("net %v", r.Net)
	}
}

// TestLocal reads /proc and /sys fixtures: whole disks only, PID 1's network namespace, the
// hottest zone, USER_HZ ticks as seconds.
func TestLocal(t *testing.T) {
	r, err := Local{Proc: "testdata/proc", Sys: "testdata/sys"}.Read(t0)
	if err != nil {
		t.Fatal(err)
	}
	if c := r.CPU["0"]; c.Busy != 90 || c.Total != 1000 {
		t.Fatalf("cpu0 %+v", c)
	}
	if c := r.CPU["1"]; c.Busy != 120 || c.Total != 1020 {
		t.Fatalf("cpu1 %+v", c)
	}
	if r.BootTime != 1761900000 || r.MemTotal != 3995648*1024 || r.MemAvailable != 2980000*1024 || r.Load5 != 0.28 {
		t.Fatalf("boot %v mem %v %v load %v", r.BootTime, r.MemTotal, r.MemAvailable, r.Load5)
	}
	if len(r.Disk) != 1 || r.Disk["vda"] != (IO{2048 * 512, 4096 * 512}) {
		t.Fatalf("disk %v", r.Disk)
	}
	if len(r.Net) != 1 || r.Net["lan0"] != (IO{1000000, 2000000}) {
		t.Fatalf("net %v", r.Net)
	}
	if r.TempC != 61.234 || r.FreqHz != 1.8e9 {
		t.Fatalf("temp %v freq %v", r.TempC, r.FreqHz)
	}
}

func TestLocalWrongMount(t *testing.T) {
	if _, err := (Local{Proc: "testdata/none"}).Read(t0); err == nil {
		t.Fatal("no error for a missing /proc")
	}
}

// TestContainers reads three containers under both cgroup drivers: RAM without inactive file cache,
// traffic of all interfaces but lo from the first process, a name from containers.txt, and the
// short id for a container the file does not list yet. The three /proc/<pid>/cgroup forms: systemd,
// systemd seen from another cgroup namespace, and cgroupfs seen from one.
func TestContainers(t *testing.T) {
	r, err := Local{Proc: "testdata/proc", Sys: "testdata/sys", Names: &Names{Path: "testdata/containers.txt"}}.Read(t0)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Containers) != 3 {
		t.Fatalf("containers %v", r.Containers)
	}
	g := r.Containers["gitea"]
	if g.CPUSec != 30 || g.RAM != 104857600 || !g.HasNet || g.Net != (IO{73000, 84000}) || !g.NameSeen {
		t.Fatalf("gitea %+v", g)
	}
	if d := r.Containers["doquiz-server"]; d.RAM != 9437184 || d.Net.In != 100 {
		t.Fatalf("doquiz-server %+v", d)
	}
	if c, ok := r.Containers["cccccccccccc"]; !ok || c.NameSeen || c.Net.In != 500 || c.CPUSec != 0.5 {
		t.Fatalf("unnamed container %+v %v", c, ok)
	}
	if n, ok := (*Names)(nil).Name("dddddddddddddddd"); ok || n != "dddddddddddd" {
		t.Fatalf("nil names: %q %v", n, ok)
	}
}

// TestNamesReload: the cron job rewrites the file; the new name shows on the next read.
func TestNamesReload(t *testing.T) {
	path := t.TempDir() + "/containers.txt"
	id := strings.Repeat("e", 64)
	os.WriteFile(path, []byte(id+" old-name\n"), 0o644)
	n := &Names{Path: path}
	if name, _ := n.Name(id); name != "old-name" {
		t.Fatalf("first %q", name)
	}
	os.WriteFile(path, []byte(id+" new-name\n"), 0o644)
	os.Chtimes(path, time.Now().Add(time.Minute), time.Now().Add(time.Minute))
	if name, _ := n.Name(id); name != "new-name" {
		t.Fatalf("after rewrite %q", name)
	}
	os.Remove(path)
	if name, ok := n.Name(id); name != "new-name" || !ok {
		t.Fatalf("file gone: keeps the last list, got %q %v", name, ok)
	}
}
