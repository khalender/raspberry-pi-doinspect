// Command doinspect measures the load of the Pi setup every 15 s and keeps it for 7 days.
// "doinspect healthcheck" checks a running server, for the container health check.
// "doinspect hash" reads a password on stdin and prints its bcrypt hash for DOINSPECT_PASSWORD_HASH.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata" // the scratch image has no zone files; TZ picks the speed test's local times

	"golang.org/x/crypto/bcrypt"

	"doinspect/server/internal/auth"
	"doinspect/server/internal/collect"
	"doinspect/server/internal/config"
	"doinspect/server/internal/netcheck"
	"doinspect/server/internal/raw"
	"doinspect/server/internal/ring"
	"doinspect/server/internal/services"
	"doinspect/server/internal/web"
)

var version = "0.4.0" // set with -ldflags "-X main.version=..."

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(healthcheck())
	}
	if len(os.Args) > 1 && os.Args[1] == "hash" {
		os.Exit(hash())
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(log); err != nil {
		log.Error("exit", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := config.FromEnv(os.Getenv)
	if err != nil {
		return err
	}
	if cfg.PasswordHash == "" && !cfg.NoLogin {
		return errors.New("set DOINSPECT_PASSWORD_HASH to a bcrypt hash of the login password, or to - to serve the pages without a login")
	}
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return err
	}
	col := &collect.Collector{
		Targets:  cfg.Targets,
		LocalNm:  cfg.LocalName,
		Store:    ring.New(cfg.Interval, cfg.Keep),
		Client:   &http.Client{Timeout: cfg.Timeout},
		SnapPath: filepath.Join(cfg.DataDir, "doinspect.snap"),
		Log:      log,
	}
	if cfg.LocalName != "" {
		col.Local = &raw.Local{Proc: cfg.Proc, Sys: cfg.Sys, FS: cfg.FS, Names: &raw.Names{Path: cfg.Names}, Pids: &raw.Pids{}}
	}
	col.Restore()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	done := make(chan struct{})
	go func() { col.Run(ctx); close(done) }()

	nc := &netcheck.Checker{Ping: cfg.Ping, DNS: cfg.DNS, DNSName: cfg.DNSName, Store: col.Store, Pinger: netcheck.ICMP{},
		Every: cfg.SpeedEvery, At: cfg.SpeedAt, Keep: 30 * 24 * time.Hour, Path: filepath.Join(cfg.DataDir, "speedtest.json"), Log: log}
	if cfg.SpeedEvery > 0 {
		nc.Speed = &netcheck.Speed{Base: cfg.SpeedURL, ISPURL: cfg.SpeedISPURL, Bytes: cfg.SpeedBytes, Streams: 4, Limit: 120 * time.Second,
			Client: &http.Client{Timeout: 120 * time.Second}, Ping: nc.PingOnce(cfg.SpeedPingName)}
	}
	go nc.Run(ctx)
	go nc.RunSpeed(ctx)
	var netView *web.Network
	if len(cfg.Ping) > 0 || len(cfg.DNS) > 0 || nc.Speed != nil {
		netView = &web.Network{Ping: cfg.Ping, DNS: cfg.DNS, Errors: nc.Errors, CPUOf: cfg.SpeedCPU, DNSName: cfg.DNSName, Keep: nc.Keep,
			Results: func() []netcheck.Result { return nil }, Running: func() bool { return false }, Next: func() time.Time { return time.Time{} }}
		if s := nc.Speed; s != nil {
			netView.Results, netView.Running = s.Results, s.Running
			netView.Next = func() time.Time { return netcheck.NextSlot(time.Now(), cfg.SpeedAt, cfg.SpeedEvery) }
		}
	}

	var list *services.List
	var svc *services.Checker
	if cfg.Services != "" {
		list = &services.List{Path: cfg.Services, Log: log}
		list.Get()
		svc = &services.Checker{List: list, Store: col.Store, Every: cfg.ServiceEvery, Timeout: 5 * time.Second, Log: log}
		go svc.Run(ctx)
	}
	var login *auth.Auth
	if !cfg.NoLogin {
		key, err := auth.LoadKey(filepath.Join(cfg.DataDir, "session.key"))
		if err != nil {
			return fmt.Errorf("session key: %w", err)
		}
		login = auth.New([]byte(cfg.PasswordHash), key, log)
	}

	mux := http.NewServeMux()
	// Open for the container health check: status and version only. The details need a session.
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		out := map[string]any{"status": "ok", "version": version}
		if login == nil || login.Valid(r) {
			out["machines"], out["series"], out["network_errors"] = col.Statuses(), len(col.Store.Keys()), nc.Errors()
		}
		json.NewEncoder(w).Encode(out)
	})
	ws := &web.Server{Store: col.Store, Statuses: col.Statuses, Now: time.Now, Version: version, Log: log, Net: netView,
		Services: list, Auth: login, Host: cfg.Host, Local: cfg.LocalName, Arcane: cfg.Arcane}
	if svc != nil {
		ws.Checks = svc.Last
	}
	ws.Register(mux)
	// The speed tests of the last 30 days, oldest first.
	mux.HandleFunc("GET /api/speedtest", func(w http.ResponseWriter, r *http.Request) {
		out := []netcheck.Result{}
		if nc.Speed != nil {
			out = append(out, nc.Speed.Results()...)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(out)
	})
	// The newest point of every series, for checks and scripts.
	mux.HandleFunc("GET /api/last", func(w http.ResponseWriter, r *http.Request) {
		out := map[string]float64{}
		for _, k := range col.Store.Keys() {
			if v, _, ok := col.Store.Last(k); ok {
				out[k] = v
			}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(out)
	})
	hs := &http.Server{Addr: cfg.Listen, Handler: ws.Guard(mux), ReadHeaderTimeout: 10 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- hs.ListenAndServe() }()
	log.Info("listening", "addr", cfg.Listen, "version", version, "interval", cfg.Interval.String(), "keep", cfg.Keep.String(),
		"targets", len(cfg.Targets), "local", cfg.LocalName,
		"ping", len(cfg.Ping), "dns", len(cfg.DNS), "speedtest_every", cfg.SpeedEvery.String(),
		"services", cfg.Services, "login", !cfg.NoLogin)

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := hs.Shutdown(shutdown); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	<-done // the collector saves its last snapshot
	log.Info("stopped")
	return nil
}

// hash prints the bcrypt hash of the first line on stdin. The password comes on stdin, never as an
// argument, so it does not show in the process list.
func hash() int {
	line, err := bufio.NewReader(io.LimitReader(os.Stdin, 1024)).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	pw := strings.TrimRight(line, "\r\n")
	if len(pw) < 8 {
		fmt.Fprintln(os.Stderr, "want a password of at least 8 characters on stdin")
		return 1
	}
	h, err := bcrypt.GenerateFromPassword([]byte(pw), 12)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Println(string(h))
	return 0
}

func healthcheck() int {
	listen := os.Getenv("DOINSPECT_LISTEN")
	if listen == "" {
		listen = ":8080"
	}
	_, port, err := net.SplitHostPort(listen)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	c := http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get("http://127.0.0.1:" + port + "/health")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintln(os.Stderr, resp.Status)
		return 1
	}
	return 0
}
