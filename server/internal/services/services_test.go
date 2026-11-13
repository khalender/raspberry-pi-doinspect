package services

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"doinspect/server/internal/ring"
)

func TestParse(t *testing.T) {
	flat := "- name: Gitea\n  url: https://gitea.home.example.com\n  group: Tools\n  container: gitea\n- name: Pi-hole\n  url: https://pihole.home.example.com/admin\n"
	s, err := Parse([]byte(flat))
	if err != nil || len(s) != 2 || s[0].Container != "gitea" || s[1].Group != "Other" {
		t.Fatalf("flat: %v %+v", err, s)
	}
	wrapped := "services:\n" + strings.ReplaceAll("  "+strings.ReplaceAll(flat, "\n", "\n  "), "\n  \n", "\n")
	if s, err := Parse([]byte(wrapped)); err != nil || len(s) != 2 {
		t.Fatalf("services key: %v %+v", err, s)
	}
	grouped := "- group: Network\n  services:\n    - name: OPNsense\n      url: https://192.0.2.1\n- name: Files\n  services:\n    - name: Drive\n      url: https://drive.home.example.com\n"
	if s, err := Parse([]byte(grouped)); err != nil || len(s) != 2 || s[0].Group != "Network" || s[1].Group != "Files" {
		t.Fatalf("grouped: %v %+v", err, s)
	}
	for _, tc := range []struct{ yml, want string }{
		{"", "empty"},
		{"[]", "no services"},
		{"- url: https://x.example\n", "no name"},
		{"- name: A\n  url: ftp://x\n", "not an http"},
		{"- name: A\n  url: https://a\n- name: A\n  url: https://b\n", "twice"},
		{"- name: [x\n", "yaml"},
		{"just text", "want a list"},
	} {
		if _, err := Parse([]byte(tc.yml)); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%q: %v, want %q", tc.yml, err, tc.want)
		}
	}
}

func TestListReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "services.yml")
	l := &List{Path: path}
	if s, err := l.Get(); err == nil || s != nil {
		t.Fatal("a missing file is not an error")
	}
	os.WriteFile(path, []byte("- name: A\n  url: https://a.example\n"), 0o600)
	if s, err := l.Get(); err != nil || len(s) != 1 {
		t.Fatalf("%v %+v", err, s)
	}
	os.WriteFile(path, []byte("- name: [broken\n"), 0o600)
	os.Chtimes(path, time.Now().Add(time.Second), time.Now().Add(time.Second))
	if s, err := l.Get(); err == nil || len(s) != 1 || s[0].Name != "A" {
		t.Fatalf("broken file: %v %+v", err, s)
	}
	os.WriteFile(path, []byte("- name: A\n  url: https://a.example\n- name: B\n  url: https://b.example\n"), 0o600)
	os.Chtimes(path, time.Now().Add(2*time.Second), time.Now().Add(2*time.Second))
	if s, err := l.Get(); err != nil || len(s) != 2 {
		t.Fatalf("fixed file: %v %+v", err, s)
	}
}

func TestCheck(t *testing.T) {
	status := func(code int) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if code == http.StatusFound {
				http.Redirect(w, r, "https://elsewhere.example/", code)
				return
			}
			w.WriteHeader(code)
		}))
	}
	ok, login, down, redirect := status(200), status(401), status(503), status(302)
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { time.Sleep(300 * time.Millisecond) }))
	tlsSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	for _, s := range []*httptest.Server{ok, login, down, redirect, slow, tlsSrv} {
		defer s.Close()
	}
	c := &Checker{Timeout: 100 * time.Millisecond, Resolve: func(ctx context.Context, host string) ([]net.IP, error) {
		if host == "public.example" {
			return []net.IP{net.ParseIP("203.0.113.9")}, nil
		}
		return []net.IP{net.ParseIP("127.0.0.1")}, nil
	}}
	ctx := context.Background()
	for _, tc := range []struct {
		url         string
		checked, up bool
		note        string
	}{
		{ok.URL, true, true, ""},
		{login.URL, true, true, ""},
		{redirect.URL, true, true, ""},
		{down.URL, true, false, "503"},
		{slow.URL, true, false, "time limit"},
		{tlsSrv.URL, true, true, "certificate not trusted"},
		{"https://public.example/", false, false, "outside the LAN"},
		{strings.Replace(ok.URL, "127.0.0.1", "app.lan", 1), true, true, ""},
	} {
		r := c.check(ctx, tc.url)
		if r.Checked != tc.checked || r.Up != tc.up || !strings.Contains(r.Note, tc.note) {
			t.Errorf("%s: %+v", tc.url, r)
		}
	}
}

func TestTick(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	path := filepath.Join(t.TempDir(), "services.yml")
	os.WriteFile(path, []byte("- name: Local\n  url: "+srv.URL+"\n- name: Out\n  url: https://203.0.113.9/\n"), 0o600)
	st := ring.New(15*time.Second, time.Hour)
	c := &Checker{List: &List{Path: path}, Store: st, Timeout: time.Second, Retry: 10 * time.Millisecond}
	now := time.Now()
	c.Tick(context.Background(), now)
	last := c.Last()
	if !last["Local"].Up || last["Out"].Checked {
		t.Fatalf("%+v", last)
	}
	if v, _, ok := st.Last("svc/http[Local].up"); !ok || v != 1 {
		t.Fatal("no up point for Local")
	}
	if _, _, ok := st.Last("svc/http[Out].up"); ok {
		t.Fatal("a link outside the LAN got a point")
	}
}

// TestRetry: a service that fails once and answers the second time counts as up.
func TestRetry(t *testing.T) {
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		if n == 1 {
			w.WriteHeader(503)
		}
	}))
	defer srv.Close()
	path := filepath.Join(t.TempDir(), "services.yml")
	os.WriteFile(path, []byte("- name: Flaky\n  url: "+srv.URL+"\n"), 0o600)
	c := &Checker{List: &List{Path: path}, Store: ring.New(15*time.Second, time.Hour), Timeout: time.Second, Retry: 10 * time.Millisecond}
	c.Tick(context.Background(), time.Now())
	if r := c.Last()["Flaky"]; !r.Up || n != 2 {
		t.Fatalf("%+v after %d requests", r, n)
	}
}
