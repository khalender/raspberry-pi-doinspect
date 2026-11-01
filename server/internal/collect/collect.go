// Package collect takes a reading of every machine once per interval, turns it into samples and
// stores them, and saves the store to a snapshot file once a minute.
package collect

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"runtime/debug"
	"sort"
	"sync"
	"time"

	"doinspect/server/internal/config"
	"doinspect/server/internal/derive"
	"doinspect/server/internal/raw"
	"doinspect/server/internal/ring"
)

// Status is what the dashboard knows about a machine: when it last reported and why the last
// attempt failed, if it did.
type Status struct {
	Name     string
	LastSeen time.Time
	Error    string
}

type Collector struct {
	Targets  []config.Target
	Local    *raw.Local // nil when doinspect reads no local figures
	LocalNm  string
	Store    *ring.Store
	Client   *http.Client
	SnapPath string
	Log      *slog.Logger

	mu       sync.Mutex
	machines map[string]*derive.Machine
	status   map[string]*Status
}

// Restore loads the snapshot, if there is one, and the last report time per machine with it.
func (c *Collector) Restore() {
	c.init()
	seen, err := c.Store.Load(c.SnapPath)
	if err != nil {
		c.Log.Warn("snapshot not loaded", "err", err)
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for name, t := range seen {
		if st, ok := c.status[name]; ok {
			st.LastSeen = t
		}
	}
	c.Log.Info("snapshot loaded", "series", len(c.Store.Keys()))
}

func (c *Collector) init() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.status != nil {
		return
	}
	c.machines, c.status = map[string]*derive.Machine{}, map[string]*Status{}
	names := []string{}
	if c.Local != nil {
		names = append(names, c.LocalNm)
	}
	for _, t := range c.Targets {
		names = append(names, t.Name)
	}
	for _, n := range names {
		c.machines[n] = &derive.Machine{}
		c.status[n] = &Status{Name: n}
	}
}

// Run collects at every interval boundary until ctx ends, then saves a last snapshot.
func (c *Collector) Run(ctx context.Context) {
	c.init()
	iv := c.Store.Interval()
	lastSave := time.Now()
	for {
		next := time.Now().Truncate(iv).Add(iv)
		select {
		case <-ctx.Done():
			c.save()
			return
		case <-time.After(time.Until(next)):
		}
		c.Tick(ctx, next)
		if time.Since(lastSave) >= time.Minute {
			c.save()
			c.Store.Prune(c.Store.Slot(next))
			lastSave = time.Now()
		}
	}
}

// Tick reads every machine for the interval that starts at t, in parallel, and stores the samples
// in t's slot.
func (c *Collector) Tick(ctx context.Context, t time.Time) {
	c.init()
	slot := c.Store.Slot(t)
	gap := 3 * c.Store.Interval().Seconds()
	var wg sync.WaitGroup
	read := func(name string, f func() (*raw.Raw, error)) {
		defer wg.Done()
		r, err := f()
		c.mu.Lock()
		defer c.mu.Unlock()
		st := c.status[name]
		if err != nil {
			st.Error = err.Error()
			c.Log.Warn("read failed", "machine", name, "err", err)
			return
		}
		st.LastSeen, st.Error = t, ""
		for _, s := range c.machines[name].Next(r, gap) {
			c.Store.Put(name+"/"+s.Name, slot, s.Value)
		}
	}
	if c.Local != nil {
		wg.Add(1)
		go read(c.LocalNm, func() (*raw.Raw, error) { return c.Local.Read(t) })
	}
	for _, tg := range c.Targets {
		wg.Add(1)
		go read(tg.Name, func() (*raw.Raw, error) { return c.scrape(ctx, tg.URL, t) })
	}
	wg.Wait()
}

func (c *Collector) scrape(ctx context.Context, url string, t time.Time) (*raw.Raw, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/plain")
	resp, err := c.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	return raw.FromExporter(io.LimitReader(resp.Body, 16<<20), t)
}

// Statuses lists the machines in a stable order: the local machine first, then the targets.
func (c *Collector) Statuses() []Status {
	c.init()
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Status, 0, len(c.status))
	for _, s := range c.status {
		out = append(out, *s)
	}
	order := map[string]int{c.LocalNm: -1}
	for i, t := range c.Targets {
		order[t.Name] = i
	}
	sort.Slice(out, func(i, j int) bool { return order[out[i].Name] < order[out[j].Name] })
	return out
}

func (c *Collector) save() {
	seen := map[string]time.Time{}
	for _, s := range c.Statuses() {
		if !s.LastSeen.IsZero() {
			seen[s.Name] = s.LastSeen
		}
	}
	n, err := c.Store.Save(c.SnapPath, seen)
	if err != nil {
		c.Log.Error("snapshot failed", "err", err)
		return
	}
	// Hand the encoder's buffers back to the system at once; otherwise the process keeps them
	// until the next collection and the container shows several times its working size.
	debug.FreeOSMemory()
	c.Log.Debug("snapshot written", "bytes", n)
}
