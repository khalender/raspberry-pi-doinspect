// Package config reads the settings from DOINSPECT_* environment variables.
package config

import (
	"fmt"
	"net/url"
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
	if len(c.Targets) == 0 && c.LocalName == "" {
		return c, fmt.Errorf("nothing to measure: set DOINSPECT_TARGETS or DOINSPECT_LOCAL")
	}
	return c, nil
}
