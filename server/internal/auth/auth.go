// Package auth is doinspect's own login (D10, F14, F31): one password checked against a bcrypt
// hash, a signed session cookie, and a wait after repeated failures from one address.
package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// CookieName is the session cookie.
const CookieName = "doinspect_session"

// Auth checks the password and the session cookie.
type Auth struct {
	Hash     []byte        // bcrypt hash of the password
	Key      []byte        // signs the cookie
	TTL      time.Duration // how long a session lasts
	MaxFails int           // failed logins from one address before it waits
	Wait     time.Duration // how long it waits
	Now      func() time.Time
	Log      *slog.Logger

	mu    sync.Mutex
	fails map[string]*fail
}

type fail struct {
	n     int
	first time.Time
}

// New sets the defaults of D10: 30 days, 5 failures, 1 minute.
func New(hash, key []byte, log *slog.Logger) *Auth {
	return &Auth{Hash: hash, Key: key, TTL: 30 * 24 * time.Hour, MaxFails: 5, Wait: time.Minute, Now: time.Now, Log: log}
}

// LoadKey reads the session key from path, or writes a new random one there (mode 0600).
func LoadKey(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err == nil && len(b) >= 32 {
		return b, nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	b = make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return nil, err
	}
	return b, nil
}

func (a *Auth) sign(exp int64) string {
	m := hmac.New(sha256.New, a.Key)
	fmt.Fprintf(m, "v1|%d", exp)
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// Valid is true when r carries a session cookie that is signed with the key and not expired.
func (a *Auth) Valid(r *http.Request) bool {
	c, err := r.Cookie(CookieName)
	if err != nil {
		return false
	}
	exp, sig, ok := strings.Cut(c.Value, ".")
	if !ok {
		return false
	}
	n, err := strconv.ParseInt(exp, 10, 64)
	if err != nil || a.Now().Unix() >= n {
		return false
	}
	return hmac.Equal([]byte(sig), []byte(a.sign(n)))
}

func secure(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}

// Start sets a new session cookie on w.
func (a *Auth) Start(w http.ResponseWriter, r *http.Request) {
	exp := a.Now().Add(a.TTL).Unix()
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: strconv.FormatInt(exp, 10) + "." + a.sign(exp), Path: "/",
		MaxAge: int(a.TTL.Seconds()), HttpOnly: true, Secure: secure(r), SameSite: http.SameSiteStrictMode})
}

// End clears the session cookie.
func (a *Auth) End(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: secure(r), SameSite: http.SameSiteStrictMode})
}

// ClientIP is the address the request came from. Only Traefik reaches the container (no published
// port), and Traefik appends the address it saw to X-Forwarded-For, so the last entry is the one
// to trust; an entry the client wrote itself sits before it.
func ClientIP(r *http.Request) string {
	if xf := r.Header.Get("X-Forwarded-For"); xf != "" {
		parts := strings.Split(xf, ",")
		if ip := strings.TrimSpace(parts[len(parts)-1]); net.ParseIP(ip) != nil {
			return ip
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// ErrWait means the address failed too often and must wait.
var ErrWait = errors.New("too many failed logins, wait a minute")

// ErrWrong means the password is wrong.
var ErrWrong = errors.New("wrong password")

// Check tests password for the address ip and counts failures.
func (a *Auth) Check(ip, password string) error {
	now := a.Now()
	a.mu.Lock()
	if a.fails == nil {
		a.fails = map[string]*fail{}
	}
	f := a.fails[ip]
	if f != nil && now.Sub(f.first) >= a.Wait {
		delete(a.fails, ip)
		f = nil
	}
	if f != nil && f.n >= a.MaxFails {
		a.mu.Unlock()
		a.Log.Warn("login refused, waiting", "ip", ip)
		return ErrWait
	}
	a.mu.Unlock()

	if bcrypt.CompareHashAndPassword(a.Hash, []byte(password)) == nil {
		a.mu.Lock()
		delete(a.fails, ip)
		a.mu.Unlock()
		a.Log.Info("login", "ip", ip)
		return nil
	}
	a.mu.Lock()
	if f = a.fails[ip]; f == nil {
		f = &fail{first: now}
		a.fails[ip] = f
	}
	f.n++
	n := f.n
	// Drop old entries so the map cannot grow without bound.
	for k, v := range a.fails {
		if now.Sub(v.first) >= a.Wait {
			delete(a.fails, k)
		}
	}
	a.mu.Unlock()
	a.Log.Warn("login failed", "ip", ip, "failures", n)
	return ErrWrong
}

// SameOrigin is false when a form POST names an Origin other than the page's own host. Browsers
// send Origin on every POST; a missing one (curl) is let through, the password still has to match.
func SameOrigin(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		return true
	}
	u, err := url.Parse(o)
	return err == nil && u.Host == r.Host
}

// Next is where to go after login: a local path only, never another site.
func Next(v string) string {
	if !strings.HasPrefix(v, "/") || strings.HasPrefix(v, "//") || strings.HasPrefix(v, "/\\") || strings.ContainsAny(v, "\r\n") {
		return "/"
	}
	return v
}
