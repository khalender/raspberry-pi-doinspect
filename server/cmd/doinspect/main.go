// Command doinspect measures the load of the Pi setup every 15 s and keeps it for 7 days.
// "doinspect healthcheck" checks a running server, for the container health check.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"doinspect/server/internal/collect"
	"doinspect/server/internal/config"
	"doinspect/server/internal/raw"
	"doinspect/server/internal/ring"
)

var version = "0.1.0" // set with -ldflags "-X main.version=..."

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(healthcheck())
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

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"status": "ok", "version": version, "machines": col.Statuses(), "series": len(col.Store.Keys())})
	})
	// The newest point of every series, for checks and scripts; the pages come in T03.
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
	hs := &http.Server{Addr: cfg.Listen, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- hs.ListenAndServe() }()
	log.Info("listening", "addr", cfg.Listen, "version", version, "interval", cfg.Interval.String(), "keep", cfg.Keep.String(),
		"targets", len(cfg.Targets), "local", cfg.LocalName)

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
