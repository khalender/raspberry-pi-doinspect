// Package services reads the service list (services.yml of the Gitea repository "dashboard") and
// checks once a minute whether each service answers (doinspect D9, F24, F30).
package services

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"go.yaml.in/yaml/v3"

	"doinspect/server/internal/ring"
)

// Prefix starts the series of the checks: svc/http[<name>].up (0 or 1) and .ms.
const Prefix = "svc/"

// Service is one entry of services.yml.
type Service struct {
	Name        string `yaml:"name"`
	URL         string `yaml:"url"`
	Description string `yaml:"description"`
	Group       string `yaml:"group"`
	Note        string `yaml:"note"`
	Container   string `yaml:"container"` // the container that serves it, if it runs on the Docker VM
}

// Key is the start of the service's series names.
func (s Service) Key() string { return Prefix + "http[" + s.Name + "]" }

// Parse reads services.yml. It takes a list of entries, a map with a "services" list, or a list of
// groups that each hold a "services" list; an entry needs a name and an http or https URL.
func Parse(b []byte) ([]Service, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(b, &root); err != nil {
		return nil, err
	}
	if len(root.Content) == 0 {
		return nil, errors.New("the file is empty")
	}
	var out []Service
	if err := collect(root.Content[0], "", &out); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, errors.New("no services in the file")
	}
	seen := map[string]bool{}
	for i, s := range out {
		u, err := url.Parse(s.URL)
		switch {
		case strings.TrimSpace(s.Name) == "":
			return nil, fmt.Errorf("entry %d has no name", i+1)
		case err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "":
			return nil, fmt.Errorf("%s: url %q is not an http or https address", s.Name, s.URL)
		case seen[s.Name]:
			return nil, fmt.Errorf("%s: named twice", s.Name)
		}
		seen[s.Name] = true
		if out[i].Group == "" {
			out[i].Group = "Other"
		}
	}
	return out, nil
}

func collect(n *yaml.Node, group string, out *[]Service) error {
	switch n.Kind {
	case yaml.MappingNode:
		var probe struct {
			Name     string    `yaml:"name"`
			Group    string    `yaml:"group"`
			Services yaml.Node `yaml:"services"`
		}
		if err := n.Decode(&probe); err != nil {
			return err
		}
		if probe.Services.Kind != 0 {
			g := probe.Group
			if g == "" {
				g = probe.Name
			}
			if g == "" {
				g = group
			}
			return collect(&probe.Services, g, out)
		}
		var s Service
		if err := n.Decode(&s); err != nil {
			return err
		}
		if s.Group == "" {
			s.Group = group
		}
		*out = append(*out, s)
	case yaml.SequenceNode:
		for _, c := range n.Content {
			if err := collect(c, group, out); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("line %d: want a list of services", n.Line)
	}
	return nil
}

// List is services.yml, read again when its modification time changes. A file that does not parse
// keeps the last good list; Err says why.
type List struct {
	Path string
	Log  *slog.Logger

	mu    sync.Mutex
	mod   time.Time
	size  int64
	items []Service
	err   error
}

// Get returns the list and the error of the last read, if any.
func (l *List) Get() ([]Service, error) {
	if l == nil || l.Path == "" {
		return nil, nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	fi, err := os.Stat(l.Path)
	if err != nil {
		l.err = fmt.Errorf("services.yml: %w", err)
		return l.items, l.err
	}
	if fi.ModTime().Equal(l.mod) && fi.Size() == l.size {
		return l.items, l.err
	}
	l.mod, l.size = fi.ModTime(), fi.Size()
	b, err := os.ReadFile(l.Path)
	if err == nil {
		var items []Service
		if items, err = Parse(b); err == nil {
			l.items, l.err = items, nil
			if l.Log != nil {
				l.Log.Info("services loaded", "path", l.Path, "services", len(items))
			}
			return l.items, nil
		}
	}
	l.err = fmt.Errorf("services.yml: %w", err)
	if l.Log != nil {
		l.Log.Warn("services.yml not loaded, keeping the last list", "err", err)
	}
	return l.items, l.err
}

// Result is the last check of one service.
type Result struct {
	Checked bool // false for a link outside the LAN, which is never requested
	Up      bool
	Status  int // HTTP status, 0 when nothing answered
	Ms      float64
	Note    string // such as "certificate not trusted" or the error
	At      time.Time
}

// Checker requests every service URL once per Every and stores the result.
type Checker struct {
	List    *List
	Store   *ring.Store
	Every   time.Duration
	Timeout time.Duration
	Log     *slog.Logger
	// Retry is the wait before a second try at a service that failed; 0 is 2 s, below 0 no retry.
	Retry time.Duration
	// Resolve looks up a host; nil uses the system resolver. Tests replace it.
	Resolve func(ctx context.Context, host string) ([]net.IP, error)

	mu   sync.Mutex
	last map[string]Result
}

func (c *Checker) retry() time.Duration {
	if c.Retry > 0 {
		return c.Retry
	}
	return 2 * time.Second
}

// Last returns the newest result of every service, by name.
func (c *Checker) Last() map[string]Result {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]Result, len(c.last))
	for k, v := range c.last {
		out[k] = v
	}
	return out
}

// Run checks every service once per Every until ctx ends.
func (c *Checker) Run(ctx context.Context) {
	if c.Every <= 0 {
		return
	}
	for {
		c.Tick(ctx, time.Now())
		next := time.Now().Truncate(c.Every).Add(c.Every)
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(next)):
		}
	}
}

// Tick checks every service in parallel and stores the results in t's slot.
func (c *Checker) Tick(ctx context.Context, t time.Time) {
	items, _ := c.List.Get()
	slot := c.Store.Slot(t)
	res := make(map[string]Result, len(items))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, s := range items {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := c.check(ctx, s.URL)
			if r.Checked && !r.Up && c.Retry >= 0 {
				// One blip (a restart, a dropped packet) is not an outage: ask once more.
				select {
				case <-ctx.Done():
				case <-time.After(c.retry()):
					r = c.check(ctx, s.URL)
				}
			}
			r.At = t
			if r.Checked {
				up := 0.0
				if r.Up {
					up = 1
					c.Store.Put(s.Key()+".ms", slot, r.Ms)
				}
				c.Store.Put(s.Key()+".up", slot, up)
			}
			mu.Lock()
			res[s.Name] = r
			mu.Unlock()
		}()
	}
	wg.Wait()
	c.mu.Lock()
	c.last = res
	c.mu.Unlock()
}

// lan is true for addresses inside the home network: private, loopback, link-local, and the
// 100.64.0.0/10 range NetBird uses.
func lan(ip net.IP) bool {
	if ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return true
	}
	_, cgnat, _ := net.ParseCIDR("100.64.0.0/10")
	return cgnat.Contains(ip)
}

func (c *Checker) resolve(ctx context.Context, host string) ([]net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		return []net.IP{ip}, nil
	}
	if c.Resolve != nil {
		return c.Resolve(ctx, host)
	}
	as, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	ips := make([]net.IP, 0, len(as))
	for _, a := range as {
		ips = append(ips, a.IP)
	}
	return ips, err
}

// lanAddrs resolves host and returns its addresses when every one of them is on the LAN.
func (c *Checker) lanAddrs(ctx context.Context, host string) ([]net.IP, error) {
	ips, err := c.resolve(ctx, host)
	if err != nil {
		return nil, err
	}
	if len(ips) == 0 {
		return nil, errors.New("no address")
	}
	for _, ip := range ips {
		if !lan(ip) {
			return nil, errOutside
		}
	}
	return ips, nil
}

var errOutside = errors.New("outside the LAN")

// check requests url once. Any status below 500 counts as up: 401 means a login stands in front,
// and a redirect is not followed. A certificate the container does not trust still means the
// service answered (OPNsense has its own), so it counts as up with a note.
func (c *Checker) check(ctx context.Context, raw string) Result {
	u, err := url.Parse(raw)
	if err != nil {
		return Result{Checked: true, Note: err.Error()}
	}
	host := u.Hostname()
	if _, err := c.lanAddrs(ctx, host); errors.Is(err, errOutside) {
		return Result{Note: "outside the LAN, not checked"}
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	dialer := &net.Dialer{Timeout: timeout}
	tr := &http.Transport{
		Proxy: nil,
		// Dial only LAN addresses, also when the name resolves differently a moment later.
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			h, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			ips, err := c.lanAddrs(ctx, h)
			if err != nil {
				return nil, err
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
		},
		TLSHandshakeTimeout: timeout,
		DisableKeepAlives:   true,
	}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return Result{Checked: true, Note: err.Error()}
	}
	req.Header.Set("User-Agent", "doinspect")
	start := time.Now()
	resp, err := client.Do(req)
	ms := float64(time.Since(start).Microseconds()) / 1000
	if err != nil {
		var cv *tls.CertificateVerificationError
		if errors.As(err, &cv) {
			return Result{Checked: true, Up: true, Ms: ms, Note: "certificate not trusted"}
		}
		return Result{Checked: true, Ms: ms, Note: short(err)}
	}
	resp.Body.Close()
	r := Result{Checked: true, Status: resp.StatusCode, Ms: ms, Up: resp.StatusCode < 500}
	if !r.Up {
		r.Note = resp.Status
	}
	return r
}

// short drops the "Get "url": " that net/http puts in front of every error.
func short(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	if errors.Is(err, context.DeadlineExceeded) || os.IsTimeout(err) {
		return "no answer within the time limit"
	}
	// "dial tcp 192.0.2.1:443: connect: connection refused" says only "connection refused".
	var se *os.SyscallError
	if errors.As(err, &se) {
		return se.Err.Error()
	}
	var de *net.DNSError
	if errors.As(err, &de) {
		return "name not found"
	}
	return err.Error()
}
