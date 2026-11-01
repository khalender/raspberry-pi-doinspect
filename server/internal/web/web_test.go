package web

import (
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"doinspect/server/internal/collect"
	"doinspect/server/internal/ring"
)

func TestDownsample(t *testing.T) {
	nan := float32(math.NaN())
	bs, step := downsample([]float32{1, 3, nan, nan, 10, 2, 4}, 3)
	if step != 3 || len(bs) != 3 {
		t.Fatalf("step %d, %d buckets", step, len(bs))
	}
	if b := bs[0]; !b.OK || b.Avg != 2 || b.Peak != 3 {
		t.Fatalf("first %+v", b)
	}
	if b := bs[1]; !b.OK || b.Avg != 6 || b.Peak != 10 {
		t.Fatalf("second skips NaN: %+v", b)
	}
	if bs2, _ := downsample([]float32{nan, nan}, 300); bs2[0].OK || bs2[1].OK {
		t.Fatalf("all NaN: %+v", bs2)
	}
}

// TestChartGap: a run of empty steps breaks the line into two paths' worth of moves.
func TestChartGap(t *testing.T) {
	bs := []Bucket{{1, 2, true}, {2, 3, true}, {}, {}, {4, 5, true}, {5, 6, true}}
	svg := string(svgChart(bs, chartOpts{W: 300, H: 60, Max: 10, Small: true}))
	line := svg[strings.Index(svg, `class="line" d="`):]
	if n := strings.Count(line[:strings.Index(line, `"/>`)], "M"); n != 2 {
		t.Fatalf("%d moves in the line, want 2: %s", n, line)
	}
	if niceMax(bs, 0) != 10 || niceMax([]Bucket{{Peak: 230, OK: true}}, 0) != 500 || niceMax(nil, 80) != 100 {
		t.Fatal("niceMax")
	}
}

type fixture struct {
	srv *Server
	h   http.Handler
}

// newFixture fills a store with a day of points for two machines, one of them silent for the last
// hour, and two containers, one gone.
func newFixture(t *testing.T) fixture {
	st := ring.New(15*time.Second, 7*24*time.Hour)
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	end := st.Slot(now)
	for slot := end - 5760; slot <= end; slot++ {
		x := float64(slot % 100)
		st.Put("pi-host/cpu", slot, 10+x/10)
		st.Put("pi-host/cpu[0]", slot, 10)
		st.Put("pi-host/ram.used", slot, 5e9)
		st.Put("pi-host/ram.total", slot, 8e9)
		st.Put("pi-host/load1", slot, 0.7)
		st.Put("pi-host/temp", slot, 54)
		st.Put("pi-host/net[br0].rx", slot, 1500)
		st.Put("pi-host/uptime", slot, 400000)
		if slot < end-240 {
			st.Put("opnsense/cpu", slot, 4)
		}
		st.Put("docker-vm/ctr[gitea].ram", slot, 150<<20)
		st.Put("docker-vm/ctr[gitea].cpu", slot, 0.8)
		st.Put("docker-vm/ctr[gitea].rx", slot, 1200)
		st.Put("docker-vm/ctr[gitea].tx", slot, 900)
		if slot < end-500 {
			st.Put("docker-vm/ctr[old-job].ram", slot, 20<<20)
		}
	}
	srv := &Server{
		Store: st, Now: func() time.Time { return now.Add(7 * time.Second) }, Version: "test",
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Statuses: func() []collect.Status {
			return []collect.Status{{Name: "pi-host", LastSeen: now}, {Name: "opnsense", LastSeen: now.Add(-time.Hour), Error: "timeout"}}
		},
	}
	mux := http.NewServeMux()
	srv.Register(mux)
	return fixture{srv, mux}
}

func (f fixture) get(t *testing.T, path string) (int, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
	return rec.Code, rec.Body.String()
}

func TestPages(t *testing.T) {
	f := newFixture(t)
	code, body := f.get(t, "/")
	if code != 200 {
		t.Fatalf("overview %d", code)
	}
	for _, want := range []string{"pi-host", "[x] reporting", "opnsense", "[!] missing", "last report 1h 00m ago", "4.66 GiB", "of 7.45 GiB", "54 °C",
		"1 of 2 machines reporting", "gitea", "150 MiB", "1.17 KiB/s / 900 B/s", "old-job", "<svg", `aria-current="true">24h<`, "one point per 5 min"} {
		if !strings.Contains(body, want) {
			t.Errorf("overview lacks %q", want)
		}
	}
	if i, j := strings.Index(body, ">gitea<"), strings.Index(body, ">old-job<"); i < 0 || j < i {
		t.Error("a gone container sorts before a live one")
	}
	if _, body := f.get(t, "/?range=1h"); !strings.Contains(body, "one point per 15 s<") {
		t.Errorf("1h range step: %s", between2(body, "status", "</span>"))
	}
	if _, body := f.get(t, "/?range=bogus"); !strings.Contains(body, `aria-current="true">24h<`) {
		t.Error("unknown range is not 24h")
	}
	code, body = f.get(t, "/machine/pi-host?range=7d")
	if code != 200 || !strings.Contains(body, "Temperature") || !strings.Contains(body, "the Pi slows down at 80 °C") || !strings.Contains(body, "Network br0 in") || !strings.Contains(body, "-7d") {
		t.Fatalf("machine page %d", code)
	}
	if code, _ := f.get(t, "/machine/nope"); code != 404 {
		t.Fatalf("unknown machine %d", code)
	}
	if code, body := f.get(t, "/containers"); code != 200 || !strings.Contains(body, "gitea") {
		t.Fatalf("containers %d", code)
	}
	if code, _ := f.get(t, "/static/style.css"); code != 200 {
		t.Fatalf("css %d", code)
	}
}

func between2(s, a, b string) string {
	i := strings.Index(s, a)
	if i < 0 {
		return ""
	}
	j := strings.Index(s[i:], b)
	if j < 0 {
		return s[i:]
	}
	return s[i : i+j]
}
