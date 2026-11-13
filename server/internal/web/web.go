// Package web draws the pages: HTML from html/template with SVG charts made on the server, no
// JavaScript (D1, D9). Pages: / (home: summary, services, machines, containers, network), /apps,
// /machines, /machine/{name}, /network, and the login page (D10).
package web

import (
	"embed"
	"html/template"
	"io/fs"
	"log/slog"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"doinspect/server/internal/auth"
	"doinspect/server/internal/collect"
	"doinspect/server/internal/ring"
	"doinspect/server/internal/services"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

// Server holds what the pages read: the store, the machines' status, the services, and the clock.
type Server struct {
	Store    *ring.Store
	Statuses func() []collect.Status
	Now      func() time.Time
	Version  string
	Log      *slog.Logger
	Net      *Network // nil without network checks

	Services *services.List                    // nil without a service list
	Checks   func() map[string]services.Result // the newest check of each service; nil without checks
	Auth     *auth.Auth                        // nil serves every page without a login
	Host     string                            // the machine the VMs run on, for the memory card
	Local    string                            // the machine doinspect runs on (the Docker VM)
	Arcane   string                            // link on the summary card; empty takes the service named Arcane

	tmpl *template.Template
}

// Range is one of the time spans a page can show (F8).
type Range struct {
	Name  string
	Span  time.Duration
	Ticks []string
}

var ranges = []Range{
	{"1h", time.Hour, []string{"-60m", "-45m", "-30m", "-15m", "now"}},
	{"6h", 6 * time.Hour, []string{"-6h", "-4.5h", "-3h", "-1.5h", "now"}},
	{"24h", 24 * time.Hour, []string{"-24h", "-18h", "-12h", "-6h", "now"}},
	{"7d", 7 * 24 * time.Hour, []string{"-7d", "-5d", "-3.5d", "-2d", "now"}},
}

// points is how many steps a line chart draws at most.
const points = 300

// Register adds the pages and the static files to mux.
func (s *Server) Register(mux *http.ServeMux) {
	s.tmpl = template.Must(template.New("").Funcs(template.FuncMap{"lower": strings.ToLower, "fill": fill}).ParseFS(templateFS, "templates/*.html"))
	static, _ := fs.Sub(staticFS, "static")
	files := http.FileServer(http.FS(static))
	mux.Handle("GET /static/", http.StripPrefix("/static/", cacheFor(files, 24*time.Hour)))
	// Browsers and tools that ask for /favicon.ico without reading the page get the same icon.
	mux.HandleFunc("GET /favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/static/icon.svg", http.StatusMovedPermanently)
	})
	mux.HandleFunc("GET /{$}", s.home)
	mux.HandleFunc("GET /apps", s.apps)
	mux.HandleFunc("GET /machines", s.machines)
	mux.HandleFunc("GET /containers", func(w http.ResponseWriter, r *http.Request) {
		to := "/apps"
		if q := r.URL.RawQuery; q != "" {
			to += "?" + q
		}
		http.Redirect(w, r, to, http.StatusMovedPermanently)
	})
	mux.HandleFunc("GET /machine/{name}", s.machine)
	mux.HandleFunc("GET /network", s.network)
	mux.HandleFunc("POST /login", s.login)
	mux.HandleFunc("POST /logout", s.logout)
}

// Guard lets a request through when it has a session or asks for something open: /health,
// /static/, /favicon.ico and the login form. Any other page shows the login page with the service
// links (F31); /api/ answers 401.
func (s *Server) Guard(next http.Handler) http.Handler {
	if s.Auth == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if p == "/health" || strings.HasPrefix(p, "/static/") || p == "/favicon.ico" || (p == "/login" && r.Method == http.MethodPost) || s.Auth.Valid(r) {
			next.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(p, "/api/") || r.Method != http.MethodGet {
			http.Error(w, "login needed", http.StatusUnauthorized)
			return
		}
		s.loginPage(w, r, r.URL.RequestURI(), "", http.StatusUnauthorized)
	})
}

func cacheFor(h http.Handler, d time.Duration) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age="+itoa(int(d.Seconds())))
		h.ServeHTTP(w, r)
	})
}

// page is what every page's template gets.
type page struct {
	Title, Lead      string
	Path             string // for the range links
	Range            Range
	Ranges           []Range
	Nav              string
	StepLabel        string
	Reporting, Total int
	LastPoint        string
	Version          string
	HasNet           bool
	LoggedIn         bool
	Clock, Date      string
	Series           int
	Body             any
}

func (s *Server) base(r *http.Request, title, nav string) (page, Range, int64, int64) {
	rg := ranges[2]
	for _, x := range ranges {
		if x.Name == r.URL.Query().Get("range") {
			rg = x
		}
	}
	now := s.Now()
	to := s.Store.Slot(now)
	n := int64(rg.Span / s.Store.Interval())
	if n > int64(s.Store.Len()) {
		n = int64(s.Store.Len())
	}
	from := to - n + 1
	step := (int(n) + points - 1) / points
	p := page{Title: title, Path: r.URL.Path, Range: rg, Ranges: ranges, Nav: nav, Version: s.Version, Series: len(s.Store.Keys()), HasNet: s.Net != nil,
		LoggedIn: s.Auth != nil, Clock: now.Format("15:04"), Date: now.Format("Mon 2 Jan"),
		StepLabel: "one point per " + every(time.Duration(step)*s.Store.Interval()) + map[bool]string{true: "", false: " (average, band = peak)"}[step == 1]}
	var newest time.Time
	for _, st := range s.Statuses() {
		p.Total++
		if s.reporting(st) {
			p.Reporting++
		}
		if st.LastSeen.After(newest) {
			newest = st.LastSeen
		}
	}
	p.LastPoint = ago(now, newest)
	return p, rg, from, to
}

// reporting is true when the machine's last report is at most two intervals old (F10).
func (s *Server) reporting(st collect.Status) bool {
	return !st.LastSeen.IsZero() && s.Now().Sub(st.LastSeen) <= 2*s.Store.Interval()+s.Store.Interval()/2
}

func (s *Server) render(w http.ResponseWriter, name string, p page) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, name, p); err != nil {
		s.Log.Error("render", "page", name, "err", err)
	}
}

func (s *Server) series(key string, from, to int64) []Bucket {
	bs, _ := downsample(s.Store.Range(key, from, to), points)
	return bs
}

func (s *Server) last(key string) (float64, bool) {
	v, _, ok := s.Store.Last(key)
	return v, ok
}

// ratio is 100 × a / b per stored point, for RAM in percent of the total.
func (s *Server) ratio(a, b string, from, to int64) []Bucket {
	av, bv := s.Store.Range(a, from, to), s.Store.Range(b, from, to)
	if av == nil || bv == nil {
		return nil
	}
	out := make([]float32, len(av))
	for i := range av {
		out[i] = float32(100 * float64(av[i]) / float64(bv[i]))
	}
	bs, _ := downsample(out, points)
	return bs
}

func (s *Server) cores(machine string) int {
	n := 0
	for _, k := range s.Store.Keys() {
		if strings.HasPrefix(k, machine+"/cpu[") {
			n++
		}
	}
	return n
}

func (s *Server) fullestFS(machine string) (mnt string, used, size float64, ok bool) {
	for _, k := range s.Store.Keys() {
		if rest, found := strings.CutPrefix(k, machine+"/fs["); found && strings.HasSuffix(rest, "].used") {
			m := strings.TrimSuffix(rest, "].used")
			if u, ok2 := s.last(k); ok2 && (!ok || u > used) {
				z, _ := s.last(machine + "/fs[" + m + "].size")
				mnt, used, size, ok = m, u, z, true
			}
		}
	}
	return
}

func keepLabel(st *ring.Store) string {
	d := time.Duration(st.Len()) * st.Interval()
	if d%(24*time.Hour) == 0 {
		return itoa(int(d/(24*time.Hour))) + " days"
	}
	return d.String()
}

// --- containers ---

type ctrRow struct {
	Name, Machine      string
	Service, URL       string // the service of services.yml that names this container
	CPU, RAM, RX, TX   string
	RAMShare           float64
	Gone               bool
	Since              string
	CPUSpark, RAMSpark template.HTML
	ramBytes           float64
	goneFor            time.Duration
}

// ctrRows lists the containers of every machine, biggest RAM first; a container without a point
// for more than two intervals sorts last and shows when it last reported.
func (s *Server) ctrRows(rg Range, from, to int64) []ctrRow {
	now := s.Store.Slot(s.Now())
	var rows []ctrRow
	max := 0.0
	for _, k := range s.Store.Keys() {
		machine, rest, _ := strings.Cut(k, "/")
		name, ok := strings.CutPrefix(rest, "ctr[")
		if !ok || !strings.HasSuffix(name, "].ram") {
			continue
		}
		name = strings.TrimSuffix(name, "].ram")
		pre := machine + "/ctr[" + name + "]"
		v, slot, ok := s.Store.Last(pre + ".ram")
		if !ok {
			continue
		}
		row := ctrRow{Name: name, Machine: machine, ramBytes: v, RAM: bytes(v)}
		if now-slot > 2 {
			// A container reports under its id until containers.txt names it, within a minute
			// (D5). That id series then stops while the same container goes on under its name,
			// so a gone id is not a container that stopped: leave it out.
			if isID(name) {
				continue
			}
			row.Gone, row.Since = true, ago(s.Now(), s.Store.SlotTime(slot))
			row.goneFor = s.Now().Sub(s.Store.SlotTime(slot))
		}
		if c, ok := s.last(pre + ".cpu"); ok {
			row.CPU = pct(c)
		}
		rx, ok1 := s.last(pre + ".rx")
		tx, ok2 := s.last(pre + ".tx")
		if ok1 && ok2 {
			row.RX, row.TX = rate(rx), rate(tx)
		}
		row.CPUSpark = svgChart(s.series(pre+".cpu", from, to), chartOpts{W: 140, H: 28, Small: true})
		row.RAMSpark = svgChart(s.series(pre+".ram", from, to), chartOpts{W: 140, H: 28, Small: true})
		if !row.Gone && v > max {
			max = v
		}
		rows = append(rows, row)
	}
	for i := range rows {
		if !rows[i].Gone && max > 0 {
			rows[i].RAMShare = math.Round(1000*rows[i].ramBytes/max) / 10
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Gone != rows[j].Gone {
			return !rows[i].Gone
		}
		return rows[i].ramBytes > rows[j].ramBytes
	})
	return rows
}

// --- one machine ---

type panel struct {
	Title, Now string
	Chart      template.HTML
}

func (s *Server) machine(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	found := false
	for _, st := range s.Statuses() {
		found = found || st.Name == name
	}
	if !found {
		http.NotFound(w, r)
		return
	}
	p, rg, from, to := s.base(r, name, "machines")
	m := name + "/"
	big := func(key string, o chartOpts) template.HTML {
		o.W, o.H, o.Ticks = 520, 180, rg.Ticks
		return svgChart(s.series(key, from, to), o)
	}
	now := func(key string, f func(float64) string) string {
		if v, ok := s.last(key); ok {
			return "now " + f(v)
		}
		return "no data"
	}
	var panels []panel
	add := func(title, key string, f func(float64) string, o chartOpts) {
		if _, ok := s.last(key); !ok && s.Store.Range(key, from, to) == nil {
			return
		}
		o.Format = f
		pk := ""
		if p, ok := peak(s.series(key, from, to)); ok {
			pk = " · peak " + f(p)
		}
		panels = append(panels, panel{Title: title, Now: now(key, f) + pk, Chart: big(key, o)})
	}
	add("CPU, all cores", m+"cpu", pct, chartOpts{Max: 100})
	add("RAM used", m+"ram.used", bytes, chartOpts{Max: func() float64 { v, _ := s.last(m + "ram.total"); return v }()})
	add("Load, 1 min", m+"load1", load, chartOpts{})
	add("Temperature", m+"temp", celsius, chartOpts{Max: 90, Limit: 80, LimitLabel: "the Pi slows down at 80 °C"})
	add("Swap used", m+"swap.used", bytes, chartOpts{})
	for _, k := range s.Store.Keys() {
		rest, ok := strings.CutPrefix(k, m)
		if !ok {
			continue
		}
		switch {
		case strings.HasPrefix(rest, "disk[") && strings.HasSuffix(rest, ".write"):
			add("Disk "+between(rest)+" writes", k, rate, chartOpts{})
		case strings.HasPrefix(rest, "disk[") && strings.HasSuffix(rest, ".read"):
			add("Disk "+between(rest)+" reads", k, rate, chartOpts{})
		case strings.HasPrefix(rest, "net[") && strings.HasSuffix(rest, ".rx"):
			add("Network "+between(rest)+" in", k, rate, chartOpts{})
		case strings.HasPrefix(rest, "net[") && strings.HasSuffix(rest, ".tx"):
			add("Network "+between(rest)+" out", k, rate, chartOpts{})
		case strings.HasPrefix(rest, "fs[") && strings.HasSuffix(rest, ".used"):
			add("File system "+between(rest)+" used", k, pct, chartOpts{Max: 100})
		}
	}
	p.Lead = "Every figure this machine reports."
	p.Body = panels
	s.render(w, "machine.html", p)
}

// isID is true for a bare container id as cgroups name it: 12 or 64 hex characters.
func isID(s string) bool {
	if len(s) != 12 && len(s) != 64 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func between(s string) string {
	_, rest, _ := strings.Cut(s, "[")
	name, _, _ := strings.Cut(rest, "]")
	return name
}

func itoa(n int) string { return strconv.Itoa(n) }
