package auth

import (
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.key")
	a, err := LoadKey(path)
	if err != nil || len(a) != 32 {
		t.Fatalf("%v %d", err, len(a))
	}
	fi, _ := os.Stat(path)
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", fi.Mode())
	}
	b, _ := LoadKey(path)
	if string(a) != string(b) {
		t.Fatal("the key changed on the second start")
	}
}

func TestClientIP(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "172.18.0.2:5555"
	if ClientIP(r) != "172.18.0.2" {
		t.Fatal(ClientIP(r))
	}
	// The client wrote its own entry; Traefik appended the real one.
	r.Header.Set("X-Forwarded-For", "10.9.9.9, 192.0.2.50")
	if ClientIP(r) != "192.0.2.50" {
		t.Fatal(ClientIP(r))
	}
}

func TestNext(t *testing.T) {
	for in, want := range map[string]string{"/machines?range=7d": "/machines?range=7d", "": "/", "//evil.example": "/", "https://evil.example": "/", "/\\evil": "/", "/a\r\nSet-Cookie: x": "/"} {
		if got := Next(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

func TestFailuresPerAddress(t *testing.T) {
	a := New([]byte("$2a$04$invalidinvalidinvalidinvalidinvalidinvalidinvalidinva"), make([]byte, 32), slog.New(slog.NewTextHandler(io.Discard, nil)))
	for i := 0; i < 5; i++ {
		if err := a.Check("192.0.2.1", "x"); err != ErrWrong {
			t.Fatalf("try %d: %v", i, err)
		}
	}
	if err := a.Check("192.0.2.1", "x"); err != ErrWait {
		t.Fatalf("sixth: %v", err)
	}
	if err := a.Check("192.0.2.2", "x"); err != ErrWrong {
		t.Fatalf("another address waits too: %v", err)
	}
}
