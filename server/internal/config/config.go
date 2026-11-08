// Package config reads the settings from DOINSPECT_* environment variables.
package config

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Target is a node_exporter to scrape: the machine's name as the dashboard shows it, and its URL.
type Target struct{ Name, URL string }

type Config struct {
	Listen    string
	DataDir   string        // the snapshot file lives here
	Interval  time.Duration // one point per series per interval
	Keep      time.Duration // how long points are kept
	Timeout   time.Duration // per scrape
	Targets   []Target
	LocalName string   // name of the machine doinspect runs on; empty reads no local figures
	Proc, Sys string   // that machine's /proc and /sys, mounted read-only
	FS        []string // paths whose file system to report for that machine
	Names     string   // container id and name list, written by a cron job on that machine (D5)

	// Network health (D6, D7).
	Ping          []Target      // name and address to ping each interval
	DNS           []Target      // name and host:port of each DNS server to time
	DNSName       string        // the name looked up
	SpeedEvery    time.Duration // 0 runs no speed test
	SpeedAt       time.Duration // time of day of the first test, local time
	SpeedBytes    int64         // each way
	SpeedURL      string        // Cloudflare's speed test
	SpeedISPURL   string        // RIPEstat, for the ISP behind the public address
	SpeedPingName string        // ping target timed before and during the test
	SpeedCPU      string        // machine whose CPU peak the page shows next to each test
}

// FromEnv builds a Config from getenv (os.Getenv in production).
func FromEnv(getenv func(string) string) (Config, error) {
	str := func(key, def string) string {
		if v := getenv(key); v != "" {
			return v
		}
		return def
	}
	dur := func(key string, def time.Duration) (time.Duration, error) {
		v := getenv(key)
		if v == "" {
			return def, nil
		}
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return 0, fmt.Errorf("%s: not a duration such as 15s: %q", key, v)
		}
		return d, nil
	}
	c := Config{
		Listen:    str("DOINSPECT_LISTEN", ":8080"),
		DataDir:   str("DOINSPECT_DATA", "/data"),
		LocalName: str("DOINSPECT_LOCAL", "docker-vm"),
		Proc:      str("DOINSPECT_PROC", "/host/proc"),
		Sys:       str("DOINSPECT_SYS", "/host/sys"),
		Names:     str("DOINSPECT_NAMES", "/data/containers.txt"),
	}
	if getenv("DOINSPECT_LOCAL") == "-" {
		c.LocalName = ""
	}
	var err error
	if c.Interval, err = dur("DOINSPECT_INTERVAL", 15*time.Second); err != nil {
		return c, err
	}
	if c.Interval%time.Second != 0 {
		return c, fmt.Errorf("DOINSPECT_INTERVAL must be whole seconds: %s", c.Interval)
	}
	if c.Keep, err = dur("DOINSPECT_KEEP", 7*24*time.Hour); err != nil {
		return c, err
	}
	if c.Keep < c.Interval {
		return c, fmt.Errorf("DOINSPECT_KEEP %s is shorter than DOINSPECT_INTERVAL %s", c.Keep, c.Interval)
	}
	if c.Timeout, err = dur("DOINSPECT_TIMEOUT", 5*time.Second); err != nil {
		return c, err
	}
	for _, p := range strings.Split(str("DOINSPECT_FS", "/data"), ",") {
		if p = strings.TrimSpace(p); p != "" {
			c.FS = append(c.FS, p)
		}
	}
	// DOINSPECT_TARGETS: name=url pairs, comma separated, e.g.
	// pi-host=http://192.0.2.10:9100/metrics,opnsense=http://192.0.2.1:9100/metrics
	seen := map[string]bool{c.LocalName: c.LocalName != ""}
	for _, part := range strings.Split(getenv("DOINSPECT_TARGETS"), ",") {
		if part = strings.TrimSpace(part); part == "" {
			continue
		}
		name, raw, ok := strings.Cut(part, "=")
		u, err := url.Parse(raw)
		if !ok || name == "" || err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return c, fmt.Errorf("DOINSPECT_TARGETS: want name=http://host:9100/metrics, got %q", part)
		}
		if seen[name] {
			return c, fmt.Errorf("DOINSPECT_TARGETS: machine %q twice", name)
		}
		seen[name] = true
		c.Targets = append(c.Targets, Target{Name: name, URL: raw})
	}
	if c.Ping, err = pairs(getenv("DOINSPECT_PING"), "DOINSPECT_PING", "name=address", func(v string) bool { return v != "" && !strings.Contains(v, "/") }); err != nil {
		return c, err
	}
	if c.DNS, err = pairs(getenv("DOINSPECT_DNS"), "DOINSPECT_DNS", "name=host:53", func(v string) bool { _, port, err := net.SplitHostPort(v); return err == nil && port != "" }); err != nil {
		return c, err
	}
	c.DNSName = str("DOINSPECT_DNS_NAME", "cloudflare.com")
	if v := getenv("DOINSPECT_SPEEDTEST_EVERY"); v == "0" {
		c.SpeedEvery = 0
	} else if c.SpeedEvery, err = dur("DOINSPECT_SPEEDTEST_EVERY", 6*time.Hour); err != nil {
		return c, err
	} else if c.SpeedEvery < time.Minute || c.SpeedEvery > 24*time.Hour {
		return c, fmt.Errorf("DOINSPECT_SPEEDTEST_EVERY must be between 1m and 24h or 0: %s", c.SpeedEvery)
	}
	at, err := time.Parse("15:04", str("DOINSPECT_SPEEDTEST_AT", "03:00"))
	if err != nil {
		return c, fmt.Errorf("DOINSPECT_SPEEDTEST_AT: want a time such as 03:00, got %q", getenv("DOINSPECT_SPEEDTEST_AT"))
	}
	c.SpeedAt = time.Duration(at.Hour())*time.Hour + time.Duration(at.Minute())*time.Minute
	c.SpeedBytes = 100_000_000
	if v := getenv("DOINSPECT_SPEEDTEST_BYTES"); v != "" {
		if c.SpeedBytes, err = strconv.ParseInt(v, 10, 64); err != nil || c.SpeedBytes < 1_000_000 {
			return c, fmt.Errorf("DOINSPECT_SPEEDTEST_BYTES: want a number of bytes of at least 1000000, got %q", v)
		}
	}
	c.SpeedURL = strings.TrimSuffix(str("DOINSPECT_SPEEDTEST_URL", "https://speed.cloudflare.com"), "/")
	c.SpeedISPURL = str("DOINSPECT_ISP_URL", "https://stat.ripe.net/data/prefix-overview/data.json")
	c.SpeedPingName = str("DOINSPECT_SPEEDTEST_PING", "internet")
	c.SpeedCPU = str("DOINSPECT_SPEEDTEST_CPU", "pi-host")

	if len(c.Targets) == 0 && c.LocalName == "" {
		return c, fmt.Errorf("nothing to measure: set DOINSPECT_TARGETS or DOINSPECT_LOCAL")
	}
	return c, nil
}

// pairs reads name=value pairs, comma separated, and checks each value with ok.
func pairs(v, key, want string, ok func(string) bool) ([]Target, error) {
	var out []Target
	seen := map[string]bool{}
	for _, part := range strings.Split(v, ",") {
		if part = strings.TrimSpace(part); part == "" {
			continue
		}
		name, val, found := strings.Cut(part, "=")
		if !found || name == "" || !ok(val) {
			return nil, fmt.Errorf("%s: want %s, got %q", key, want, part)
		}
		if seen[name] {
			return nil, fmt.Errorf("%s: %q twice", key, name)
		}
		seen[name] = true
		out = append(out, Target{Name: name, URL: val})
	}
	return out, nil
}
