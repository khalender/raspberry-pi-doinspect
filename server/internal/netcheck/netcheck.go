package netcheck

import (
	"context"
	"log/slog"
	"math"
	"net"
	"strings"
	"sync"
	"time"

	"doinspect/server/internal/config"
	"doinspect/server/internal/ring"
)

// Series names: net/ping[<name>].rtt and .jitter (ms), .loss (%); net/dns[<name>].ms, NaN when the
// lookup failed, and .fail (0 or 1).
const Prefix = "net/"

// Checker pings and times DNS every interval and runs the speed test at its times of day.
type Checker struct {
	Ping    []config.Target
	DNS     []config.Target
	DNSName string
	Store   *ring.Store
	Pinger  Pinger
	Speed   *Speed // nil runs no speed test
	Every   time.Duration
	At      time.Duration // time of day of the first test, local time
	Keep    time.Duration // speed test results
	Path    string        // speed test results file
	Log     *slog.Logger

	Count   int           // echo requests per round, default 10
	Gap     time.Duration // between them, default 100 ms
	Timeout time.Duration // for the last reply and for a DNS lookup

	mu   sync.Mutex
	errs map[string]string
}

func (c *Checker) defaults() {
	if c.Count == 0 {
		c.Count = 10
	}
	if c.Gap == 0 {
		c.Gap = 100 * time.Millisecond
	}
	if c.Timeout == 0 {
		c.Timeout = time.Second
	}
}

// Run measures at every interval boundary until ctx ends.
func (c *Checker) Run(ctx context.Context) {
	c.defaults()
	if len(c.Ping) == 0 && len(c.DNS) == 0 {
		return
	}
	iv := c.Store.Interval()
	for {
		next := time.Now().Truncate(iv).Add(iv)
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(next)):
		}
		c.Tick(ctx, next)
	}
}

// Tick pings every target and times every DNS server in parallel and stores the results in t's
// slot.
func (c *Checker) Tick(ctx context.Context, t time.Time) {
	c.defaults()
	slot := c.Store.Slot(t)
	var wg sync.WaitGroup
	for _, tg := range c.Ping {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rtt, err := c.Pinger.Ping(ctx, tg.URL, c.Count, c.Gap, c.Timeout)
			c.setErr("ping "+tg.Name, err)
			if err != nil {
				return
			}
			r := Stats(rtt)
			key := Prefix + "ping[" + tg.Name + "]"
			put := func(k string, v float64) {
				if !math.IsNaN(v) {
					c.Store.Put(k, slot, v)
				}
			}
			put(key+".rtt", r.RTT)
			put(key+".jitter", r.Jitter)
			put(key+".loss", r.Loss)
		}()
	}
	for _, tg := range c.DNS {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ms, err := Lookup(ctx, tg.URL, c.DNSName, 2*c.Timeout)
			c.setErr("dns "+tg.Name, err)
			key := Prefix + "dns[" + tg.Name + "]"
			if err != nil {
				c.Store.Put(key+".fail", slot, 1)
				return
			}
			c.Store.Put(key+".ms", slot, ms)
			c.Store.Put(key+".fail", slot, 0)
		}()
	}
	wg.Wait()
}

func (c *Checker) setErr(what string, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.errs == nil {
		c.errs = map[string]string{}
	}
	if err == nil {
		delete(c.errs, what)
		return
	}
	if c.errs[what] != err.Error() {
		c.Log.Warn("network check failed", "check", what, "err", err)
	}
	c.errs[what] = err.Error()
}

// Errors lists the last error of each failing check, such as "ping isp".
func (c *Checker) Errors() map[string]string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]string, len(c.errs))
	for k, v := range c.errs {
		out[k] = v
	}
	return out
}

// Lookup times one lookup of name through the DNS server at addr (host:port), in milliseconds.
func Lookup(ctx context.Context, addr, name string, timeout time.Duration) (float64, error) {
	r := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, addr)
	}}
	if !strings.HasSuffix(name, ".") {
		name += "." // no search domains
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	start := time.Now()
	if _, err := r.LookupIPAddr(ctx, name); err != nil {
		return 0, err
	}
	return float64(time.Since(start)) / float64(time.Millisecond), nil
}

// PingOnce is one round to the target named name, for the speed test's latency.
func (c *Checker) PingOnce(name string) func(ctx context.Context) Round {
	c.defaults()
	for _, tg := range c.Ping {
		if tg.Name == name {
			return func(ctx context.Context) Round {
				rtt, err := c.Pinger.Ping(ctx, tg.URL, c.Count, c.Gap, c.Timeout)
				if err != nil {
					return Round{RTT: math.NaN(), Jitter: math.NaN(), Loss: math.NaN()}
				}
				return Stats(rtt)
			}
		}
	}
	return nil
}

// RunSpeed runs the speed test at each of its times of day until ctx ends (D7). A failed test is
// not repeated before the next time.
func (c *Checker) RunSpeed(ctx context.Context) {
	if c.Speed == nil || c.Every <= 0 {
		return
	}
	if err := c.Speed.Load(c.Path); err != nil {
		c.Log.Warn("speed test results not loaded", "err", err)
	}
	for {
		next := NextSlot(time.Now(), c.At, c.Every)
		c.Log.Info("next speed test", "at", next.Format(time.RFC3339))
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(next)):
		}
		r, ok := c.Speed.Run(ctx, time.Now)
		if !ok {
			continue
		}
		if ctx.Err() != nil {
			return
		}
		c.Log.Info("speed test", "down_mbit", math.Round(r.Down/1e5)/10, "up_mbit", math.Round(r.Up/1e5)/10,
			"idle_ms", r.Idle, "loaded_ms", r.Loaded, "isp", r.ISP, "asn", r.ASN, "site", r.Site, "err", r.Err)
		c.Speed.Prune(time.Now().Add(-c.Keep))
		if err := c.Speed.Save(c.Path); err != nil {
			c.Log.Error("speed test results not saved", "err", err)
		}
	}
}

// NextSlot is the first time after now at at past local midnight plus a whole number of every,
// within the same day: with at 03:00 and every 6h, 03:00, 09:00, 15:00 and 21:00 local time,
// also on the days daylight saving time starts or ends.
func NextSlot(now time.Time, at, every time.Duration) time.Time {
	y, m, d := now.Date()
	for day := -1; day <= 2; day++ {
		for off := at % every; off < 24*time.Hour; off += every {
			h, mi := int(off/time.Hour), int(off%time.Hour/time.Minute)
			t := time.Date(y, m, d+day, h, mi, 0, 0, now.Location())
			if t.After(now) {
				return t
			}
		}
	}
	return now.Add(every) // not reached: every is at most 24h
}
