package netcheck

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"doinspect/server/internal/config"
	"doinspect/server/internal/ring"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func ms(v ...float64) []time.Duration {
	out := make([]time.Duration, len(v))
	for i, x := range v {
		if x < 0 {
			out[i] = Lost
		} else {
			out[i] = time.Duration(x * float64(time.Millisecond))
		}
	}
	return out
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestStats(t *testing.T) {
	// Round trips 10, lost, 14, 12, lost: average 12, jitter (|14-10| + |12-14|) / 2 = 3, 40 % lost.
	r := Stats(ms(10, -1, 14, 12, -1))
	if !near(r.RTT, 12) || !near(r.Jitter, 3) || !near(r.Loss, 40) {
		t.Fatalf("%+v", r)
	}
	if r := Stats(ms(-1, -1)); !math.IsNaN(r.RTT) || !math.IsNaN(r.Jitter) || r.Loss != 100 {
		t.Fatalf("all lost: %+v", r)
	}
	if r := Stats(ms(7)); r.RTT != 7 || r.Jitter != 0 || r.Loss != 0 {
		t.Fatalf("one reply: %+v", r)
	}
}

type fakePinger map[string][]time.Duration

func (f fakePinger) Ping(_ context.Context, addr string, _ int, _, _ time.Duration) ([]time.Duration, error) {
	if r, ok := f[addr]; ok {
		return r, nil
	}
	return nil, fmt.Errorf("unreachable %s", addr)
}

// dnsServer answers A queries for good.test. and refuses everything else; "slow" makes it not
// answer at all.
func dnsServer(t *testing.T, slow bool) string {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	go func() {
		buf := make([]byte, 512)
		for {
			n, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			if slow || n < 12 {
				continue
			}
			q := buf[:n]
			// The question ends after the name's zero byte plus type and class.
			end := 12
			for end < n && q[end] != 0 {
				end += int(q[end]) + 1
			}
			end += 5
			name := strings.ToLower(string(q[13 : end-5]))
			qtype := int(q[end-4])<<8 | int(q[end-3])
			resp := append([]byte{}, q[:end]...)
			resp[2] = 0x81
			if strings.Contains(name, "good") && qtype == 1 {
				resp[3] = 0x80
				resp[7] = 1 // one answer
				resp = append(resp, 0xc0, 12, 0, 1, 0, 1, 0, 0, 0, 60, 0, 4, 192, 0, 2, 7)
			} else if strings.Contains(name, "good") {
				resp[3] = 0x80 // no AAAA, no error
			} else {
				resp[3] = 0x83 // NXDOMAIN
			}
			pc.WriteTo(resp, from)
		}
	}()
	return pc.LocalAddr().String()
}

func TestTick(t *testing.T) {
	st := ring.New(15*time.Second, time.Hour)
	ok, slow := dnsServer(t, false), dnsServer(t, true)
	c := &Checker{
		Ping:    []config.Target{{Name: "gateway", URL: "gw"}, {Name: "isp", URL: "down"}, {Name: "far", URL: "lossy"}},
		DNS:     []config.Target{{Name: "pihole", URL: ok}, {Name: "slow", URL: slow}},
		DNSName: "good.test",
		Store:   st,
		Pinger:  fakePinger{"gw": ms(1, 2, 3), "lossy": ms(-1, -1)},
		Timeout: 200 * time.Millisecond,
		Log:     quiet,
	}
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	c.Tick(context.Background(), now)
	last := func(k string) (float64, bool) { v, _, ok := st.Last(k); return v, ok }
	if v, _ := last("net/ping[gateway].rtt"); v != 2 {
		t.Fatalf("gateway rtt %v", v)
	}
	if v, _ := last("net/ping[gateway].jitter"); v != 1 {
		t.Fatalf("gateway jitter %v", v)
	}
	if _, ok := last("net/ping[isp].rtt"); ok {
		t.Fatal("unreachable target stored a round trip")
	}
	if v, _ := last("net/ping[far].loss"); v != 100 {
		t.Fatalf("lossy loss %v", v)
	}
	if _, ok := last("net/ping[far].rtt"); ok {
		t.Fatal("all lost stored a round trip")
	}
	if v, ok := last("net/dns[pihole].ms"); !ok || v <= 0 || v > 1000 {
		t.Fatalf("dns ms %v %v", v, ok)
	}
	if v, _ := last("net/dns[pihole].fail"); v != 0 {
		t.Fatal("good lookup counted as failed")
	}
	if v, _ := last("net/dns[slow].fail"); v != 1 {
		t.Fatal("timed out lookup not counted as failed")
	}
	if _, ok := last("net/dns[slow].ms"); ok {
		t.Fatal("timed out lookup stored a time")
	}
	if e := c.Errors(); e["ping isp"] == "" || e["dns slow"] == "" || e["ping gateway"] != "" {
		t.Fatalf("errors %v", e)
	}
	// NXDOMAIN is a failure too.
	if _, err := Lookup(context.Background(), ok, "missing.test", time.Second); err == nil {
		t.Fatal("NXDOMAIN gave no error")
	}
}

func TestNextSlot(t *testing.T) {
	zone, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}
	at := func(s string) time.Time {
		v, err := time.ParseInLocation("2006-01-02 15:04", s, zone)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	for _, tc := range []struct{ now, want string }{
		{"2026-11-08 00:10", "2026-11-08 03:00"},
		{"2026-11-08 03:00", "2026-11-08 09:00"}, // the slot itself is past
		{"2026-11-08 14:59", "2026-11-08 15:00"},
		{"2026-11-08 21:01", "2026-11-09 03:00"},
		// Summer time starts 2026-03-29 at 02:00 and ends 2026-10-25 at 03:00: still 03, 09 local.
		{"2026-03-29 01:00", "2026-03-29 03:00"},
		{"2026-03-29 03:30", "2026-03-29 09:00"},
		{"2026-10-25 01:00", "2026-10-25 03:00"},
		{"2026-10-25 03:30", "2026-10-25 09:00"},
	} {
		if got := NextSlot(at(tc.now), 3*time.Hour, 6*time.Hour); !got.Equal(at(tc.want)) {
			t.Errorf("after %s: %s, want %s", tc.now, got.Format("2006-01-02 15:04 MST"), tc.want)
		}
	}
	// A start time past the interval folds back: 09:00 every 6h is 03, 09, 15, 21 as well.
	if got := NextSlot(at("2026-11-08 00:10"), 9*time.Hour, 6*time.Hour); !got.Equal(at("2026-11-08 03:00")) {
		t.Errorf("09:00 every 6h: %s", got)
	}
}

// speedServer plays Cloudflare's speed test and RIPEstat. failUp makes uploads answer 500; slow
// makes downloads stall.
func speedServer(t *testing.T, failUp, slow bool) (*httptest.Server, *atomic.Int64, *atomic.Int64) {
	var down, up atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("GET /cdn-cgi/trace", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "fl=1\nip=192.0.2.44\ncolo=BRU\nloc=BE\n")
	})
	mux.HandleFunc("GET /ripe", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("resource") != "192.0.2.44" {
			http.Error(w, "bad resource", 400)
			return
		}
		io.WriteString(w, `{"data":{"asns":[{"asn":64500,"holder":"EXAMPLE-AS Example ISP"}]}}`)
	})
	mux.HandleFunc("GET /__down", func(w http.ResponseWriter, r *http.Request) {
		n, _ := strconv.ParseInt(r.URL.Query().Get("bytes"), 10, 64)
		if n > chunk {
			http.Error(w, "too big", 403)
			return
		}
		if slow {
			<-r.Context().Done()
			return
		}
		w.Header().Set("Content-Length", strconv.FormatInt(n, 10))
		k, _ := io.CopyN(w, zeros{}, n)
		down.Add(k)
	})
	mux.HandleFunc("POST /__up", func(w http.ResponseWriter, r *http.Request) {
		k, _ := io.Copy(io.Discard, r.Body)
		up.Add(k)
		if failUp {
			http.Error(w, "no", 500)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &down, &up
}

type zeros struct{}

func (zeros) Read(p []byte) (int, error) { clear(p); return len(p), nil }

func TestSpeed(t *testing.T) {
	srv, down, up := speedServer(t, false, false)
	pings := 0
	s := &Speed{Base: srv.URL, ISPURL: srv.URL + "/ripe", Bytes: 60_000_000, Streams: 4, Limit: 30 * time.Second, Client: srv.Client(),
		Ping: func(context.Context) Round {
			pings++
			time.Sleep(5 * time.Millisecond)
			return Round{RTT: float64(pings)}
		}}
	r, ok := s.Run(context.Background(), time.Now)
	if !ok || r.Err != "" {
		t.Fatalf("%v %+v", ok, r)
	}
	// 60 MB is three requests each way: 25, 25 and 10 MB.
	if r.BytesDown != 60_000_000 || r.BytesUp != 60_000_000 || down.Load() != 60_000_000 || up.Load() != 60_000_000 {
		t.Fatalf("bytes down %d/%d up %d/%d", r.BytesDown, down.Load(), r.BytesUp, up.Load())
	}
	if r.Down <= 0 || r.Up <= 0 {
		t.Fatalf("speeds %+v", r)
	}
	if r.ISP != "EXAMPLE-AS Example ISP" || r.ASN != 64500 || r.IP != "192.0.2.44" || r.Site != "BRU" {
		t.Fatalf("where %+v", r)
	}
	if r.Idle != 1 || r.Loaded <= 1 {
		t.Fatalf("latency idle %v loaded %v", r.Idle, r.Loaded)
	}
	if len(s.Results()) != 1 {
		t.Fatal("result not recorded")
	}

	// Save, load, prune.
	path := filepath.Join(t.TempDir(), "speedtest.json")
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}
	s2 := &Speed{}
	if err := s2.Load(path); err != nil || len(s2.Results()) != 1 || s2.Results()[0].ISP != r.ISP {
		t.Fatalf("load: %v %+v", err, s2.Results())
	}
	if err := s2.Load(filepath.Join(t.TempDir(), "none.json")); err != nil {
		t.Fatalf("missing file: %v", err)
	}
	s2.Prune(r.Start.Add(time.Second))
	if len(s2.Results()) != 0 {
		t.Fatal("prune kept an old result")
	}
}

func TestSpeedFails(t *testing.T) {
	srv, _, up := speedServer(t, true, false)
	s := &Speed{Base: srv.URL, Bytes: 30_000_000, Streams: 2, Limit: 30 * time.Second, Client: srv.Client()}
	r, _ := s.Run(context.Background(), time.Now)
	if !strings.Contains(r.Err, "500") || r.BytesDown != 30_000_000 || r.Down <= 0 {
		t.Fatalf("failing upload: %+v", r)
	}
	if up.Load() == 0 {
		t.Fatal("upload never sent")
	}

	srv, _, _ = speedServer(t, false, true)
	s = &Speed{Base: srv.URL, Bytes: 30_000_000, Streams: 2, Limit: 300 * time.Millisecond, Client: srv.Client()}
	start := time.Now()
	r, _ = s.Run(context.Background(), time.Now)
	if !strings.Contains(r.Err, "time limit") || time.Since(start) > 5*time.Second {
		t.Fatalf("stalled download: %+v after %s", r, time.Since(start))
	}

	// One test at a time.
	s.running.Store(true)
	if _, ok := s.Run(context.Background(), time.Now); ok {
		t.Fatal("second test ran while one was running")
	}
}
