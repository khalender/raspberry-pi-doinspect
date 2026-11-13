package web

import (
	"html/template"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"doinspect/server/internal/auth"
	"doinspect/server/internal/collect"
	"doinspect/server/internal/config"
	"doinspect/server/internal/netcheck"
	"doinspect/server/internal/ring"
	"doinspect/server/internal/services"
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
		// Recreated a minute ago: its name stops until containers.txt names the new id.
		if slot < end-4 {
			st.Put("docker-vm/ctr[pihole].ram", slot, 25<<20)
		}
		// The same container before containers.txt named it.
		if slot < end-10 {
			st.Put("docker-vm/ctr[0123456789ab].ram", slot, 30<<20)
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
		t.Fatalf("home %d", code)
	}
	for _, want := range []string{"pi-host", ">Reporting<", "opnsense", ">Missing<", "last report 1h 00m ago", "4.66 GiB / 7.45 GiB", ">54<span class=\"unit\">°C",
		"1 of 2 machines reporting", "gitea", "150 MiB", `class="ring"`, `aria-current="true">24h<`, "one point per 5 min", `aria-current="page"`,
		"opnsense has not reported since 1h 00m ago", "1 thing needs you"} {
		if !strings.Contains(body, want) {
			t.Errorf("home lacks %q", want)
		}
	}
	if strings.Contains(body, "0123456789ab") {
		t.Error("a gone container id is on the home page")
	}
	if strings.Contains(body, "pihole stopped reporting") {
		t.Error("a container silent for a minute is on the summary card")
	}
	if strings.Contains(body, "old-job stopped reporting") {
		t.Error("a container gone for hours, serving no service, is on the summary card")
	}
	if _, body := f.get(t, "/?range=1h"); !strings.Contains(body, "one point per 15 s<") {
		t.Errorf("1h range step: %s", between2(body, "status", "</p>"))
	}
	if _, body := f.get(t, "/?range=bogus"); !strings.Contains(body, `aria-current="true">24h<`) {
		t.Error("unknown range is not 24h")
	}
	code, body = f.get(t, "/apps")
	if code != 200 || !strings.Contains(body, "1.17 KiB/s / 900 B/s") || !strings.Contains(body, "old-job") || !strings.Contains(body, "No service list set up") {
		t.Fatalf("apps %d", code)
	}
	if strings.Contains(body, "0123456789ab") {
		t.Error("a gone container id is on the apps page")
	}
	if i, j := strings.Index(body, ">gitea<"), strings.Index(body, ">old-job<"); i < 0 || j < i {
		t.Error("a gone container sorts before a live one")
	}
	code, body = f.get(t, "/machines?range=7d")
	if code != 200 || !strings.Contains(body, `class="tchart"`) || !strings.Contains(body, "peak 20 %") || !strings.Contains(body, "Every figure of pi-host") {
		t.Fatalf("machines %d", code)
	}
	code, body = f.get(t, "/machine/pi-host?range=7d")
	if code != 200 || !strings.Contains(body, "Temperature") || !strings.Contains(body, "the Pi slows down at 80 °C") || !strings.Contains(body, "Network br0 in") || !strings.Contains(body, "-7d") {
		t.Fatalf("machine page %d", code)
	}
	if code, _ := f.get(t, "/machine/nope"); code != 404 {
		t.Fatalf("unknown machine %d", code)
	}
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, httptest.NewRequest("GET", "/containers?range=1h", nil))
	if rec.Code != 301 || rec.Header().Get("Location") != "/apps?range=1h" {
		t.Fatalf("containers redirect %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if code, body := f.get(t, "/static/style.css"); code != 200 || strings.Contains(body, "googleapis") || !strings.Contains(body, "nunito-sans-latin-400-normal.woff2") {
		t.Fatalf("css %d", code)
	}
	if code, _ := f.get(t, "/static/nunito-sans-latin-800-normal.woff2"); code != 200 {
		t.Fatalf("font %d", code)
	}
	if code, body := f.get(t, "/static/icon.svg"); code != 200 || !strings.HasPrefix(body, "<svg") {
		t.Fatalf("icon %d", code)
	}
	if code, _ := f.get(t, "/favicon.ico"); code != 301 {
		t.Fatalf("favicon.ico %d", code)
	}
	for _, path := range []string{"/", "/apps", "/machines", "/machine/pi-host"} {
		_, body := f.get(t, path)
		if !strings.Contains(body, `rel="icon" href="/static/icon.svg?v=test"`) || !strings.Contains(body, `href="/static/style.css?v=test"`) {
			t.Errorf("%s does not link the icon and the stylesheet with the version (cached for a day)", path)
		}
		if strings.Contains(body, "https://") {
			t.Errorf("%s loads something from outside", path)
		}
	}
}

// TestEmptyStore: every page renders before the first reading.
func TestEmptyStore(t *testing.T) {
	srv := &Server{Store: ring.New(15*time.Second, 7*24*time.Hour), Now: time.Now, Version: "test", Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Statuses: func() []collect.Status { return nil }}
	mux := http.NewServeMux()
	srv.Register(mux)
	for _, path := range []string{"/", "/apps", "/machines"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), "</html>") {
			t.Errorf("%s: %d", path, rec.Code)
		}
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

func TestNetworkPage(t *testing.T) {
	f := newFixture(t)
	if code, _ := f.get(t, "/network"); code != 404 {
		t.Fatalf("network page without checks: %d", code)
	}
	if _, body := f.get(t, "/"); strings.Contains(body, "Network →") || strings.Contains(body, `aria-label="Network"`) {
		t.Fatal("home shows a network card or link without checks")
	}
	now := f.srv.Now()
	var results []netcheck.Result
	f.srv.Net = &Network{
		Ping:    []config.Target{{Name: "gateway", URL: "192.0.2.1"}, {Name: "isp", URL: "198.51.100.1"}},
		DNS:     []config.Target{{Name: "pihole", URL: "192.0.2.53:53"}},
		Results: func() []netcheck.Result { return results },
		Running: func() bool { return false },
		Next:    func() time.Time { return now.Add(3 * time.Hour) },
		Errors:  func() map[string]string { return map[string]string{"ping isp": "unreachable"} },
		CPUOf:   "pi-host",
		DNSName: "cloudflare.com",
		Keep:    30 * 24 * time.Hour,
	}
	// No data yet: the page and the card still render.
	code, body := f.get(t, "/network")
	if code != 200 || !strings.Contains(body, "Known after the first speed test") || !strings.Contains(body, "No speed test yet") {
		t.Fatalf("empty network page %d", code)
	}
	if _, body := f.get(t, "/"); !strings.Contains(body, "Network →") || !strings.Contains(body, "provider known after the first speed test") {
		t.Fatal("home lacks the empty network card")
	}

	end := f.srv.Store.Slot(now)
	for slot := end - 5760; slot <= end; slot++ {
		f.srv.Store.Put("net/ping[gateway].rtt", slot, 1.2)
		f.srv.Store.Put("net/ping[gateway].jitter", slot, 0.3)
		f.srv.Store.Put("net/ping[gateway].loss", slot, 0)
		f.srv.Store.Put("net/dns[pihole].ms", slot, 4)
		f.srv.Store.Put("net/dns[pihole].fail", slot, float64(slot%10/9)) // one in ten fails
	}
	r := func(h int, ip string, down float64, err string) netcheck.Result {
		st := now.Add(-time.Duration(h) * time.Hour)
		return netcheck.Result{Start: st, End: st.Add(20 * time.Second), ISP: "EXAMPLE-AS Example ISP", ASN: 64500, IP: ip, Site: "BRU",
			Down: down, Up: down / 5, Idle: 3.1, Loaded: 25, BytesDown: 1e8, BytesUp: 1e8, Err: err}
	}
	results = []netcheck.Result{r(18, "192.0.2.44", 480e6, ""), r(12, "192.0.2.44", 470e6, "time limit 2m0s reached"), r(6, "192.0.2.99", 495e6, "")}
	code, body = f.get(t, "/network")
	if code != 200 {
		t.Fatalf("network page %d", code)
	}
	for _, want := range []string{"EXAMPLE-AS Example ISP", "AS64500", "192.0.2.99", "495 Mbit/s", "99.0 Mbit/s", "3.1 ms", "25 ms",
		">gateway<", "192.0.2.1", "Ping (round trip)", "now 1.2 ms", "unreachable", ">pihole<", "192.0.2.53:53", "now 4.0 ms",
		"Failed lookups in this range", "576 of 5760 (10 %)", "lookup of cloudflare.com", "changed", "time limit 2m0s reached",
		"next test", `aria-label="Network" aria-current="page"`} {
		if !strings.Contains(body, want) {
			t.Errorf("network page lacks %q", want)
		}
	}
	if strings.Contains(body, "Measured since") {
		t.Error("a range the data covers shows the measured-since note")
	}
	if _, body := f.get(t, "/network?range=7d"); !strings.Contains(body, "Measured since Sat 12:00") {
		t.Errorf("7d range lacks the measured-since note: %s", between2(body, "note", "</p>"))
	}
	// Newest first, and the address change is marked on the newest row only.
	if i, j := strings.Index(body, "495 Mbit/s</td>"), strings.Index(body, "480 Mbit/s</td>"); i < 0 || j < i {
		t.Error("speed tests not newest first")
	}
	if strings.Count(body, ">changed<") != 1 {
		t.Errorf("%d changes marked, want 1", strings.Count(body, ">changed<"))
	}
	// The Pi host's CPU during the test comes from its ring.
	if !strings.Contains(body, "Pi host CPU peak") {
		t.Error("no CPU peak for the latest test")
	}
	if _, body := f.get(t, "/"); !strings.Contains(body, "495 Mbit/s") || !strings.Contains(body, "gateway 1.2 ms") || !strings.Contains(body, "No answer from isp") {
		t.Error("home lacks the latest speed test, the gateway ping or the lost target")
	}
}

// TestServicesAndSummary: services from services.yml with their checks, the summary card, and the
// container that serves a service.
func TestServicesAndSummary(t *testing.T) {
	f := newFixture(t)
	dir := t.TempDir()
	path := dir + "/services.yml"
	yml := "- name: Gitea\n  url: https://gitea.home.example.com\n  description: git\n  group: Tools\n  container: gitea\n" +
		"- name: Arcane\n  url: https://arcane.home.example.com\n  group: Tools\n" +
		"- name: OPNsense\n  url: https://192.0.2.1\n  group: Network\n  note: LAN only\n" +
		"- name: NetBird\n  url: https://app.netbird.io\n  group: External\n"
	if err := os.WriteFile(path, []byte(yml), 0o600); err != nil {
		t.Fatal(err)
	}
	f.srv.Services = &services.List{Path: path}
	checks := map[string]services.Result{
		"Gitea":    {Checked: true, Up: true, Status: 200, Ms: 12},
		"Arcane":   {Checked: true, Status: 502, Note: "502 Bad Gateway"},
		"OPNsense": {Checked: true, Up: true, Note: "certificate not trusted"},
		"NetBird":  {Note: "outside the LAN, not checked"},
	}
	f.srv.Checks = func() map[string]services.Result { return checks }
	_, body := f.get(t, "/")
	for _, want := range []string{">Tools<", ">Network<", ">External<", "gitea.home.example.com", ">LAN only<", "(Up, certificate not trusted)", ">Link only<",
		`class="tile"`, `<svg class="px"`, ">doInspect<", "doInspect</title>",
		"Arcane does not answer", "2 of 3 checked apps answer", `href="https://arcane.home.example.com">Open Arcane`, "12 ms"} {
		if !strings.Contains(body, want) {
			t.Errorf("home lacks %q", want)
		}
	}
	_, body = f.get(t, "/apps")
	if !strings.Contains(body, `<a href="https://gitea.home.example.com">Gitea</a>`) {
		t.Error("the container table does not link gitea to its service")
	}
	// A broken file keeps the last list and says why.
	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(path, []byte("- name: [unclosed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, body = f.get(t, "/apps")
	if !strings.Contains(body, "Showing the last list that loaded") || !strings.Contains(body, "gitea.home.example.com") {
		t.Error("a broken services.yml dropped the list or hid the error")
	}
	// Everything up: the summary card is sage and says so.
	if err := os.WriteFile(path, []byte(yml), 0o600); err != nil {
		t.Fatal(err)
	}
	checks["Arcane"] = services.Result{Checked: true, Up: true, Status: 401}
	f.srv.Statuses = func() []collect.Status { return []collect.Status{{Name: "pi-host", LastSeen: f.srv.Now()}} }
	_, body = f.get(t, "/")
	if !strings.Contains(body, "All 3 apps are up,<br>nothing needs you") || strings.Contains(body, `class="hero alert"`) {
		t.Errorf("summary with nothing wrong: %s", between2(body, `class="hero`, "</h3>"))
	}
}

// TestLogin: without a session every page is the login form with the service links and no figure;
// the password starts a session; repeated failures wait; a forged cookie is refused.
func TestLogin(t *testing.T) {
	f := newFixture(t)
	dir := t.TempDir()
	os.WriteFile(dir+"/services.yml", []byte("- name: Gitea\n  url: https://gitea.home.example.com\n  group: Tools\n  container: gitea\n"), 0o600)
	f.srv.Services = &services.List{Path: dir + "/services.yml"}
	f.srv.Checks = func() map[string]services.Result {
		return map[string]services.Result{"Gitea": {Checked: true, Status: 503}}
	}
	hash, _ := bcrypt.GenerateFromPassword([]byte("right horse"), bcrypt.MinCost)
	now := f.srv.Now()
	f.srv.Auth = auth.New(hash, []byte("0123456789abcdef0123456789abcdef"), slog.New(slog.NewTextHandler(io.Discard, nil)))
	f.srv.Auth.Now = func() time.Time { return now }
	h := f.srv.Guard(f.h)
	do := func(method, path, form string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
		var r *http.Request
		if form != "" {
			r = httptest.NewRequest(method, path, strings.NewReader(form))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		} else {
			r = httptest.NewRequest(method, path, nil)
		}
		r.RemoteAddr = "192.0.2.50:1234"
		for _, c := range cookies {
			r.AddCookie(c)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec
	}
	for _, path := range []string{"/", "/machines?range=7d", "/machine/pi-host", "/apps"} {
		rec := do("GET", path, "")
		body := rec.Body.String()
		if rec.Code != 401 || !strings.Contains(body, `type="password"`) || !strings.Contains(body, "gitea.home.example.com") || !strings.Contains(body, `class="tile"`) || !strings.Contains(body, "Log in to doInspect") {
			t.Fatalf("%s without session: %d", path, rec.Code)
		}
		for _, leak := range []string{"pi-host", "150 MiB", "Down", "Up", "503", "machines reporting", `http-equiv="refresh"`} {
			if strings.Contains(body, leak) && !strings.Contains(path, leak) {
				t.Errorf("%s login page shows %q", path, leak)
			}
		}
		if !strings.Contains(body, `name="next" value="`+template.HTMLEscapeString(path)+`"`) {
			t.Errorf("%s: next not kept", path)
		}
	}
	if rec := do("GET", "/api/last", ""); rec.Code != 401 {
		t.Fatalf("api without session %d", rec.Code)
	}
	if rec := do("GET", "/static/style.css", ""); rec.Code != 200 {
		t.Fatalf("static without session %d", rec.Code)
	}
	// Wrong password, then five more: the sixth waits.
	if rec := do("POST", "/login", "password=nope&next=/machines"); rec.Code != 401 || !strings.Contains(rec.Body.String(), "Wrong password") {
		t.Fatalf("wrong password %d", rec.Code)
	}
	for i := 0; i < 4; i++ {
		do("POST", "/login", "password=nope")
	}
	if rec := do("POST", "/login", "password=right horse"); rec.Code != 429 {
		t.Fatalf("sixth try in a minute %d, want 429", rec.Code)
	}
	now = now.Add(61 * time.Second)
	rec := do("POST", "/login", "password=right horse&next=//evil.example/x")
	if rec.Code != 303 || rec.Header().Get("Location") != "/" {
		t.Fatalf("login %d to %q", rec.Code, rec.Header().Get("Location"))
	}
	cookie := rec.Result().Cookies()[0]
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.MaxAge != 30*24*3600 {
		t.Fatalf("cookie %+v", cookie)
	}
	if rec := do("GET", "/machines", "", cookie); rec.Code != 200 || !strings.Contains(rec.Body.String(), "pi-host") {
		t.Fatalf("with session %d", rec.Code)
	}
	if rec := do("POST", "/login", "password=right horse&next=/machines?range=7d"); rec.Header().Get("Location") != "/machines?range=7d" {
		t.Fatalf("next %q", rec.Header().Get("Location"))
	}
	forged := &http.Cookie{Name: auth.CookieName, Value: "9999999999.AAAA"}
	if rec := do("GET", "/", "", forged); rec.Code != 401 {
		t.Fatalf("forged cookie %d", rec.Code)
	}
	now = now.Add(31 * 24 * time.Hour)
	if rec := do("GET", "/", "", cookie); rec.Code != 401 {
		t.Fatalf("expired cookie %d", rec.Code)
	}
	// Logout clears the cookie; a POST from another site is refused.
	r := httptest.NewRequest("POST", "/logout", nil)
	r.Header.Set("Origin", "https://evil.example")
	r.AddCookie(cookie)
	rr := httptest.NewRecorder()
	f.h.ServeHTTP(rr, r)
	if rr.Code != 400 {
		t.Fatalf("cross-site logout %d", rr.Code)
	}
	rr = httptest.NewRecorder()
	f.h.ServeHTTP(rr, httptest.NewRequest("POST", "/logout", nil))
	if c := rr.Result().Cookies(); rr.Code != 303 || len(c) != 1 || c[0].MaxAge >= 0 {
		t.Fatalf("logout %d %+v", rr.Code, c)
	}
}
