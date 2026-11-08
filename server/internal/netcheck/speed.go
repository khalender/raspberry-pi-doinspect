package netcheck

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Result is one speed test (F20, F21). Speeds are in bit/s, latencies in milliseconds; a field
// the test did not reach is 0. Err is set when the test failed or hit its time limit; the speeds
// then count what was moved until then.
type Result struct {
	Start, End         time.Time
	ISP                string // the AS holder, such as "EXAMPLE-AS Example ISP"
	ASN                int
	IP                 string // public address
	Site               string // Cloudflare's site, such as BRU
	Down, Up           float64
	Idle, Loaded       float64 // ping round trip before and during the transfers
	BytesDown, BytesUp int64
	Err                string
}

// Speed runs speed tests. Base is Cloudflare's speed test (https://speed.cloudflare.com): __down
// sends the bytes asked for, at most 25 MB a request in practice (100 MB gives 403), __up takes a
// body, and /cdn-cgi/trace names the public address and the site. Cloudflare's /meta, which named
// the ISP, answers {} to programs since 2026; the ISP comes from RIPEstat (ISPURL) instead.
type Speed struct {
	Base, ISPURL string
	Bytes        int64 // each way
	Streams      int
	Limit        time.Duration // the whole test
	Client       *http.Client
	Ping         func(ctx context.Context) Round // the latency target; nil skips latency

	mu      sync.Mutex
	results []Result
	running atomic.Bool
}

const chunk = 25_000_000

// Run does one speed test and records its result. A test that is still running makes Run return
// at once with ok false.
func (s *Speed) Run(ctx context.Context, now func() time.Time) (Result, bool) {
	if !s.running.CompareAndSwap(false, true) {
		return Result{}, false
	}
	defer s.running.Store(false)
	ctx, cancel := context.WithTimeout(ctx, s.Limit)
	defer cancel()
	r := Result{Start: now()}
	err := s.run(ctx, &r)
	r.End = now()
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			err = fmt.Errorf("time limit %s reached", s.Limit)
		}
		r.Err = err.Error()
	}
	s.mu.Lock()
	s.results = append(s.results, r)
	s.mu.Unlock()
	return r, true
}

func (s *Speed) run(ctx context.Context, r *Result) error {
	if err := s.where(ctx, r); err != nil {
		return err
	}
	if s.Ping != nil {
		r.Idle = s.Ping(ctx).RTT
	}
	// Ping all through the transfers; the latency under load is the average of those rounds.
	var loaded []float64
	stop := make(chan struct{})
	pinged := make(chan struct{})
	go func() {
		defer close(pinged)
		if s.Ping == nil {
			return
		}
		for {
			select {
			case <-stop:
				return
			default:
			}
			if v := s.Ping(ctx).RTT; v == v { // not NaN
				loaded = append(loaded, v)
			} else {
				select { // a failing ping returns at once; do not spin
				case <-stop:
					return
				case <-time.After(time.Second):
				}
			}
		}
	}()
	var err error
	r.BytesDown, r.Down, err = s.transfer(ctx, false)
	if err == nil {
		r.BytesUp, r.Up, err = s.transfer(ctx, true)
	}
	close(stop)
	<-pinged
	if len(loaded) > 0 {
		sum := 0.0
		for _, v := range loaded {
			sum += v
		}
		r.Loaded = sum / float64(len(loaded))
	}
	return err
}

// where fills in the public address and site from Cloudflare, then the ISP from RIPEstat. A
// failed ISP lookup is not a failed test.
func (s *Speed) where(ctx context.Context, r *Result) error {
	body, err := s.get(ctx, s.Base+"/cdn-cgi/trace", 64<<10)
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(body), "\n") {
		k, v, _ := strings.Cut(line, "=")
		switch k {
		case "ip":
			r.IP = v
		case "colo":
			r.Site = v
		}
	}
	if r.IP == "" || s.ISPURL == "" {
		return nil
	}
	body, err = s.get(ctx, s.ISPURL+"?sourceapp=doinspect&resource="+url.QueryEscape(r.IP), 1<<20)
	if err != nil {
		return nil
	}
	var ov struct {
		Data struct {
			ASNs []struct {
				ASN    int    `json:"asn"`
				Holder string `json:"holder"`
			} `json:"asns"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &ov) == nil && len(ov.Data.ASNs) > 0 {
		r.ASN, r.ISP = ov.Data.ASNs[0].ASN, ov.Data.ASNs[0].Holder
	}
	return nil
}

func (s *Speed) get(ctx context.Context, u string, max int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", req.URL.Redacted(), resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, max))
}

// transfer moves Bytes in requests of at most 25 MB, Streams at a time, and returns the bytes moved
// and the speed in bit/s over the whole transfer.
func (s *Speed) transfer(ctx context.Context, up bool) (int64, float64, error) {
	var sizes []int64
	for left := s.Bytes; left > 0; left -= chunk {
		sizes = append(sizes, min(left, chunk))
	}
	var moved atomic.Int64
	jobs := make(chan int64)
	errs := make(chan error, len(sizes))
	start := time.Now()
	var wg sync.WaitGroup
	for range max(1, s.Streams) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := range jobs {
				if err := s.one(ctx, up, n, &moved); err != nil {
					errs <- err
				}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, n := range sizes {
			select {
			case jobs <- n:
			case <-ctx.Done():
				return
			}
		}
	}()
	wg.Wait()
	secs := time.Since(start).Seconds()
	close(errs)
	err := <-errs
	if err == nil {
		err = ctx.Err()
	}
	n := moved.Load()
	if secs <= 0 {
		return n, 0, err
	}
	return n, float64(n) * 8 / secs, err
}

func (s *Speed) one(ctx context.Context, up bool, n int64, moved *atomic.Int64) error {
	var req *http.Request
	var err error
	if up {
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, s.Base+"/__up", &counter{n: n, moved: moved})
		if err == nil {
			req.ContentLength = n
			req.Header.Set("Content-Type", "application/octet-stream")
		}
	} else {
		req, err = http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/__down?bytes=%d", s.Base, n), nil)
	}
	if err != nil {
		return err
	}
	resp, err := s.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if !up {
		_, err = io.Copy(io.Discard, &counter{r: resp.Body, n: -1, moved: moved})
	} else {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s %s: %s", req.Method, req.URL.Path, resp.Status)
	}
	return err
}

// counter counts bytes into moved. With r it passes r's bytes on; without, it produces n zero bytes
// for an upload body.
type counter struct {
	r     io.Reader
	n     int64
	moved *atomic.Int64
}

func (c *counter) Read(p []byte) (int, error) {
	if c.r != nil {
		k, err := c.r.Read(p)
		c.moved.Add(int64(k))
		return k, err
	}
	if c.n <= 0 {
		return 0, io.EOF
	}
	k := int(min(int64(len(p)), c.n))
	clear(p[:k])
	c.n -= int64(k)
	c.moved.Add(int64(k))
	return k, nil
}

// Results lists the recorded speed tests, oldest first.
func (s *Speed) Results() []Result {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Result(nil), s.results...)
}

// Running is true while a test runs.
func (s *Speed) Running() bool { return s.running.Load() }

// Prune drops results that started before cut.
func (s *Speed) Prune(cut time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := sort.Search(len(s.results), func(i int) bool { return !s.results[i].Start.Before(cut) })
	s.results = append([]Result(nil), s.results[i:]...)
}

// Save writes the results as JSON through a temporary file and a rename.
func (s *Speed) Save(path string) error {
	b, err := json.Marshal(s.Results())
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".speed-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Load reads results saved by Save. A missing file is no error.
func (s *Speed) Load(path string) error {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var rs []Result
	if err := json.Unmarshal(b, &rs); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	sort.Slice(rs, func(i, j int) bool { return rs[i].Start.Before(rs[j].Start) })
	s.mu.Lock()
	s.results = rs
	s.mu.Unlock()
	return nil
}
