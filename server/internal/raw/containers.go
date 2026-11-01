package raw

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Container is one Docker container's counters, read from its cgroup: CPU seconds and network
// bytes since it started, and the RAM it holds now.
type Container struct {
	CPUSec   float64
	RAM      float64 // memory.current minus inactive file cache, as docker stats counts it
	Net      IO
	HasNet   bool
	ShortID  string
	NameSeen bool // false when containers.txt does not list the id yet
}

// containers reads every Docker container's cgroup v2 folder, under the systemd driver
// (system.slice/docker-<id>.scope) or the cgroupfs driver (docker/<id>), and keys them by name.
// Network counters come from one of the container's processes: cgroups do not count traffic.
func (l Local) containers(r *Raw) {
	var pids map[string]string
	root := filepath.Join(l.Sys, "fs/cgroup")
	dirs, _ := filepath.Glob(filepath.Join(root, "system.slice/docker-*.scope"))
	more, _ := filepath.Glob(filepath.Join(root, "docker/*"))
	for _, dir := range append(dirs, more...) {
		id := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(dir), "docker-"), ".scope")
		if len(id) != 64 {
			continue
		}
		var c Container
		c.ShortID = id[:12]
		stat := keyed(filepath.Join(dir, "cpu.stat"))
		usec, ok := stat["usage_usec"]
		if !ok {
			continue // the container stopped while we read
		}
		c.CPUSec = usec / 1e6
		c.RAM = one(filepath.Join(dir, "memory.current"))
		if inactive, ok := keyed(filepath.Join(dir, "memory.stat"))["inactive_file"]; ok && inactive <= c.RAM {
			c.RAM -= inactive
		}
		// cgroup.procs lists pids of the reader's own pid namespace, which in the container are all
		// 0; so find a process of the container through the host's /proc/<pid>/cgroup instead.
		pid := l.Pids.get(l.Proc, id)
		if pid == "" {
			if pids == nil {
				pids = scanPids(l.Proc)
				l.Pids.set(pids)
			}
			pid = pids[id]
		}
		if pid != "" {
			l.lines(pid+"/net/dev", func(f []string) {
				if len(f) >= 10 && strings.HasSuffix(f[0], ":") && f[0] != "lo:" {
					c.Net.In += num(f[1])
					c.Net.Out += num(f[9])
					c.HasNet = true
				}
			})
		}
		name, known := l.Names.Name(id)
		c.NameSeen = known
		r.Containers[name] = c
	}
}

// keyed reads a "key value" file such as cpu.stat or memory.stat.
func keyed(path string) map[string]float64 {
	out := map[string]float64{}
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if k, v, ok := strings.Cut(sc.Text(), " "); ok {
			out[k] = num(v)
		}
	}
	return out
}

func one(path string) float64 {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	return num(strings.TrimSpace(string(b)))
}

// A container id in /proc/<pid>/cgroup: "0::/system.slice/docker-<id>.scope" under systemd,
// "0::/docker/<id>" under cgroupfs, and "0::/../<id>" when the reader sits in its own cgroup
// namespace, as doinspect's container does.
var containerID = regexp.MustCompile(`[-/]([0-9a-f]{64})(\.scope)?$`)

// scanPids maps container ids to one process of each, from /proc/<pid>/cgroup of every process
// (readable by every user). About 300 small files on the Docker VM; it runs only when a
// container's remembered process is gone.
func scanPids(proc string) map[string]string {
	out := map[string]string{}
	dirs, _ := os.ReadDir(proc)
	for _, d := range dirs {
		pid := d.Name()
		if pid[0] < '0' || pid[0] > '9' {
			continue
		}
		b, err := os.ReadFile(filepath.Join(proc, pid, "cgroup"))
		if err != nil {
			continue
		}
		if m := containerID.FindSubmatch(bytes.TrimSpace(b)); m != nil {
			if _, ok := out[string(m[1])]; !ok {
				out[string(m[1])] = pid
			}
		}
	}
	return out
}

// Pids remembers one process per container between readings. A nil *Pids remembers nothing.
type Pids struct {
	mu   sync.Mutex
	byID map[string]string
}

// get returns the remembered process of container id if it still belongs to it.
func (p *Pids) get(proc, id string) string {
	if p == nil {
		return ""
	}
	p.mu.Lock()
	pid := p.byID[id]
	p.mu.Unlock()
	if pid == "" {
		return ""
	}
	if b, err := os.ReadFile(filepath.Join(proc, pid, "cgroup")); err != nil || !strings.Contains(string(b), id) {
		return ""
	}
	return pid
}

func (p *Pids) set(m map[string]string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.byID = m
	p.mu.Unlock()
}

// Names maps container ids to names from the file a cron job on the Docker VM writes every minute
// (D5): one "<id> <name>" per line, as `docker ps --no-trunc --format '{{.ID}} {{.Names}}'` prints.
// It reads the file again when its modification time changes. A nil *Names knows no names.
type Names struct {
	Path string

	mu    sync.Mutex
	mtime time.Time
	byID  map[string]string
}

// Name returns the container's name, or the first 12 characters of its id and false when the file
// does not list it.
func (n *Names) Name(id string) (string, bool) {
	if n == nil {
		return id[:12], false
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if st, err := os.Stat(n.Path); err == nil && !st.ModTime().Equal(n.mtime) {
		if b, err := os.ReadFile(n.Path); err == nil {
			n.byID, n.mtime = map[string]string{}, st.ModTime()
			for _, line := range strings.Split(string(b), "\n") {
				if f := strings.Fields(line); len(f) == 2 {
					n.byID[f[0]] = f[1]
				}
			}
		}
	}
	if name, ok := n.byID[id]; ok {
		return name, true
	}
	return id[:12], false
}
