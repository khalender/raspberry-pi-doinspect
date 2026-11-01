package collect

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"doinspect/server/internal/config"
	"doinspect/server/internal/raw"
	"doinspect/server/internal/ring"
)

// TestTick scrapes a good exporter, a slow one and the local fixtures twice, and checks the
// stored points, the status of each machine and the snapshot written at the end of Run.
func TestTick(t *testing.T) {
	page, _ := os.ReadFile("../raw/testdata/linux.prom")
	cpu := 900.0
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// one more busy second per scrape on cpu0
		io.WriteString(w, strings.Replace(string(page), `{cpu="0",mode="user"} 50`, `{cpu="0",mode="user"} `+ftoa(cpu-850), 1))
		cpu += 1
	}))
	defer good.Close()
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
	}))
	defer slow.Close()

	dir := t.TempDir()
	c := &Collector{
		Targets:  []config.Target{{Name: "pi-host", URL: good.URL}, {Name: "opnsense", URL: slow.URL}},
		Local:    &raw.Local{Proc: "../raw/testdata/proc", Sys: "../raw/testdata/sys"},
		LocalNm:  "docker-vm",
		Store:    ring.New(15*time.Second, time.Hour),
		Client:   &http.Client{Timeout: 100 * time.Millisecond},
		SnapPath: filepath.Join(dir, "doinspect.snap"),
		Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	t0 := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	c.Tick(context.Background(), t0)
	c.Tick(context.Background(), t0.Add(15*time.Second))

	slot := c.Store.Slot(t0)
	if v, s, ok := c.Store.Last("pi-host/ram.used"); !ok || v != float64(float32(5.5e9)) || s != slot+1 {
		t.Fatalf("pi-host ram %v at %d, %v", v, s, ok)
	}
	if v, _, ok := c.Store.Last("pi-host/cpu[0]"); !ok || v <= 0 {
		t.Fatalf("pi-host cpu[0] %v %v", v, ok)
	}
	if _, _, ok := c.Store.Last("docker-vm/load1"); !ok {
		t.Fatal("no local load")
	}
	st := c.Statuses()
	if len(st) != 3 || st[0].Name != "docker-vm" || st[1].Name != "pi-host" || st[2].Name != "opnsense" {
		t.Fatalf("order %+v", st)
	}
	if !st[1].LastSeen.Equal(t0.Add(15*time.Second)) || st[1].Error != "" {
		t.Fatalf("pi-host status %+v", st[1])
	}
	if !st[2].LastSeen.IsZero() || st[2].Error == "" {
		t.Fatalf("slow target counted as seen: %+v", st[2])
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c.Run(ctx) // saves at once on a done context
	if _, err := os.Stat(c.SnapPath); err != nil {
		t.Fatalf("no snapshot: %v", err)
	}
	again := &Collector{Targets: c.Targets, LocalNm: "docker-vm", Local: c.Local, Store: ring.New(15*time.Second, time.Hour),
		SnapPath: c.SnapPath, Log: c.Log}
	again.Restore()
	if v, _, ok := again.Store.Last("pi-host/ram.used"); !ok || v != float64(float32(5.5e9)) {
		t.Fatalf("after restore %v %v", v, ok)
	}
	if st := again.Statuses(); !st[1].LastSeen.Equal(t0.Add(15 * time.Second)) {
		t.Fatalf("last seen after restore %+v", st[1])
	}
}

func ftoa(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }
