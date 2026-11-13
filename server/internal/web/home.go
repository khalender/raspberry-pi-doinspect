package web

import (
	"errors"
	"fmt"
	"html/template"
	"math"
	"net/http"
	"regexp"
	"strings"
	"time"

	"doinspect/server/internal/auth"
	"doinspect/server/internal/netcheck"
	"doinspect/server/internal/services"
)

// fills is the Pastel Outline series order.
var fills = []string{"var(--po-sage)", "var(--po-coral)", "var(--po-mustard)", "var(--po-sky)"}

func fill(i int) string { return fills[i%len(fills)] }

// --- services ---

type svcItem struct {
	Name, URL, Host, Description, Note, Container string
	Kind, State                                   string // ok, warn, bad, off; the word next to the dot
	Ms                                            string
	Initials                                      string
	Fill                                          int
}

type svcGroup struct {
	Name  string
	Items []svcItem
}

var hostRe = regexp.MustCompile(`^https?://`)

// groups sorts the services into their groups, in the order they first appear. With checks, each
// item carries its state; without (the login page), none.
func (s *Server) groups(withState bool) ([]svcGroup, int, int, error) {
	items, err := s.Services.Get()
	var checks map[string]services.Result
	if withState && s.Checks != nil {
		checks = s.Checks()
	}
	var out []svcGroup
	idx := map[string]int{}
	up, checked := 0, 0
	for i, sv := range items {
		it := svcItem{Name: sv.Name, URL: sv.URL, Host: strings.TrimSuffix(hostRe.ReplaceAllString(sv.URL, ""), "/"), Description: sv.Description,
			Note: sv.Note, Container: sv.Container, Initials: initials(sv.Name), Fill: i}
		if withState {
			r, ok := checks[sv.Name]
			switch {
			case !ok:
				it.Kind, it.State = "off", "Not checked yet"
			case !r.Checked:
				it.Kind, it.State = "off", "Link only"
			case r.Up:
				checked++
				up++
				it.Kind, it.State, it.Ms = "ok", "Up", msec(r.Ms)
				if r.Note != "" {
					it.Kind, it.State = "warn", "Up, "+r.Note
				}
			default:
				checked++
				it.Kind, it.State = "bad", "Down"
				if r.Note != "" {
					it.State += ", " + r.Note
				}
			}
		}
		g, ok := idx[sv.Group]
		if !ok {
			g = len(out)
			idx[sv.Group] = g
			out = append(out, svcGroup{Name: sv.Group})
		}
		out[g].Items = append(out[g].Items, it)
	}
	return out, up, checked, err
}

func initials(n string) string {
	f := strings.FieldsFunc(n, func(r rune) bool { return r == ' ' || r == '-' || r == '_' || r == '.' })
	if len(f) == 0 {
		return "?"
	}
	a := []rune(f[0])
	if len(f) > 1 {
		return strings.ToLower(string(a[0]) + string([]rune(f[1])[0]))
	}
	if len(a) > 1 {
		return strings.ToLower(string(a[:2]))
	}
	return strings.ToLower(string(a))
}

// --- pieces ---

// cpuRing draws the CPU share as a sage sector in an outlined circle with the percent below the centre.
func cpuRing(p float64) template.HTML {
	p = math.Max(0, math.Min(100, p))
	a := p / 100 * 2 * math.Pi
	x, y := 50+40*math.Sin(a), 50-40*math.Cos(a)
	large := 0
	if p > 50 {
		large = 1
	}
	sector := ""
	switch {
	case p >= 99.9:
		sector = `<circle cx="50" cy="50" r="40" class="sec"/>`
	case p > 0.1:
		sector = fmt.Sprintf(`<path class="sec" d="M50 50 L50 10 A40 40 0 %d 1 %.2f %.2f Z"/>`, large, x, y)
	}
	return template.HTML(fmt.Sprintf(`<svg class="ring" viewBox="0 0 100 100" role="img" aria-label="CPU %.0f percent"><circle cx="50" cy="50" r="40" class="face"/>%s<circle cx="50" cy="50" r="3" class="face"/><text x="50" y="76" text-anchor="middle">%.0f%%</text></svg>`, p, sector, p))
}

type col struct {
	H         float64 // height in percent of the chart
	Tip, Tick string
	Sel, None bool
}

type columns struct {
	Cols   []col
	Lo, Hi string
	Label  string
	N      int
}

// cols folds the points from from to to into n columns, each the peak of its part. The highest
// column is sage with its value in a tooltip; a part without points is a dashed stub.
func (s *Server) cols(vals []float32, from int64, rg Range, n int, top float64, f func(float64) string, lo, hi string) columns {
	out := columns{Lo: lo, Hi: hi, N: n}
	if len(vals) == 0 {
		return out
	}
	per := float64(len(vals)) / float64(n)
	peaks := make([]float64, n)
	ok := make([]bool, n)
	for i := 0; i < n; i++ {
		a, b := int(float64(i)*per), int(float64(i+1)*per)
		for _, v := range vals[a:min(b, len(vals))] {
			fv := float64(v)
			if math.IsNaN(fv) {
				continue
			}
			if !ok[i] || fv > peaks[i] {
				peaks[i], ok[i] = fv, true
			}
		}
	}
	if top <= 0 {
		for i := range peaks {
			if ok[i] && peaks[i] > top {
				top = peaks[i]
			}
		}
		top *= 1.15
		if top <= 0 {
			top = 1
		}
	}
	sel := -1
	for i := range peaks {
		if ok[i] && (sel < 0 || peaks[i] >= peaks[sel]) {
			sel = i
		}
	}
	// About six labels: HH:MM is wider than an hour or a weekday.
	every := max(1, n/12)
	if rg.Name == "1h" || rg.Name == "6h" {
		every = max(1, n/6)
	}
	loc := s.Now().Location()
	for i := 0; i < n; i++ {
		c := col{None: !ok[i], Sel: i == sel, H: 6}
		if ok[i] {
			c.H = math.Max(6, math.Min(100, peaks[i]/top*100))
		}
		if c.Sel {
			c.Tip = f(peaks[i])
			out.Label = "peak " + c.Tip
		}
		if i%every == 0 {
			t := s.Store.SlotTime(from + int64(float64(i)*per)).In(loc)
			switch rg.Name {
			case "7d":
				c.Tick = t.Format("Mon")[:2]
			case "24h":
				c.Tick = t.Format("15")
			default:
				c.Tick = t.Format("15:04")
			}
		}
		out.Cols = append(out.Cols, c)
	}
	return out
}

func (s *Server) ratioRaw(a, b string, from, to int64) []float32 {
	av, bv := s.Store.Range(a, from, to), s.Store.Range(b, from, to)
	if av == nil || bv == nil {
		return nil
	}
	out := make([]float32, len(av))
	for i := range av {
		out[i] = float32(100 * float64(av[i]) / float64(bv[i]))
	}
	return out
}

// --- machines ---

type bar struct {
	Label, Right, Cls string
	Pct               float64
	PctText           string
}

type stat struct{ V, Unit, Cap string }

type mcard struct {
	Name, Meta, Since string
	OK                bool
	Kind, State       string
	Ring              template.HTML
	HasCPU            bool
	Bars              []bar
	Stats             []stat
	CPUCols, RAMCols  columns
}

func splitUnit(v string) (string, string) {
	a, b, ok := strings.Cut(v, " ")
	if !ok {
		return v, ""
	}
	return a, b
}

func (s *Server) mcards(rg Range, from, to int64, n int) []mcard {
	var out []mcard
	for _, st := range s.Statuses() {
		m := st.Name + "/"
		c := mcard{Name: st.Name, OK: s.reporting(st), Since: ago(s.Now(), st.LastSeen), Kind: "ok", State: "Reporting"}
		if !c.OK {
			c.Kind, c.State = "bad", "Missing"
		}
		var meta []string
		if n := s.cores(st.Name); n > 0 {
			meta = append(meta, itoa(n)+" cores")
		}
		if v, ok := s.last(m + "ram.total"); ok {
			meta = append(meta, bytes(v))
		}
		c.Meta = strings.Join(meta, " · ")
		if v, ok := s.last(m + "cpu"); ok {
			c.Ring, c.HasCPU = cpuRing(v), true
		}
		used, ok1 := s.last(m + "ram.used")
		total, ok2 := s.last(m + "ram.total")
		if ok1 && ok2 && total > 0 {
			p := 100 * used / total
			cls := "sky"
			if p > 85 {
				cls = "coral"
			}
			c.Bars = append(c.Bars, bar{Label: "Memory", Right: bytes(used) + " / " + bytes(total), Pct: math.Round(p), PctText: pct(p), Cls: cls})
		}
		if mnt, used, size, ok := s.fullestFS(st.Name); ok {
			cls := "sage"
			if used > 85 {
				cls = "coral"
			}
			c.Bars = append(c.Bars, bar{Label: "Disk " + mnt, Right: bytes(size*used/100) + " / " + bytes(size), Pct: math.Round(used), PctText: pct(used), Cls: cls})
		}
		if t, ok := s.last(m + "temp"); ok {
			c.Stats = append(c.Stats, stat{fmt.Sprintf("%.0f", t), "°C", "Temperature"})
			if t >= 80 {
				c.Kind, c.State = "warn", "Hot"
			}
		}
		if l, ok := s.last(m + "load5"); ok {
			c.Stats = append(c.Stats, stat{load(l), "", "Load 5 min"})
		} else if l, ok := s.last(m + "load1"); ok {
			c.Stats = append(c.Stats, stat{load(l), "", "Load 1 min"})
		}
		if up, ok := s.last(m + "uptime"); ok {
			v, u := splitUnit(uptime(up))
			if strings.HasSuffix(v, "h") {
				v, u = strings.TrimSuffix(v, "h"), "h"
			}
			c.Stats = append(c.Stats, stat{v, u, "Uptime"})
		}
		if n > 0 {
			c.CPUCols = s.cols(s.Store.Range(m+"cpu", from, to), from, rg, n, 100, pct, "0 %", "100 %")
			c.RAMCols = s.cols(s.ratioRaw(m+"ram.used", m+"ram.total", from, to), from, rg, n, 100, pct, "0 %", "100 %")
		}
		out = append(out, c)
	}
	return out
}

// --- summary ---

type hero struct {
	Problems          []string
	More              int
	AppsUp, AppsTotal int
	Containers        int
	Newest, NewestAgo string
	Arcane            string
}

// problems lists what needs the user (F26).
func (s *Server) problems(groups []svcGroup, svcErr error, rows []ctrRow) []string {
	var out []string
	if svcErr != nil {
		out = append(out, "Service list: "+svcErr.Error())
	}
	for _, g := range groups {
		for _, it := range g.Items {
			if it.Kind == "bad" {
				out = append(out, it.Name+" does not answer")
			}
		}
	}
	for _, st := range s.Statuses() {
		if !s.reporting(st) {
			out = append(out, st.Name+" has not reported since "+ago(s.Now(), st.LastSeen))
			continue
		}
		if t, ok := s.last(st.Name + "/temp"); ok && t >= 80 {
			out = append(out, st.Name+" at "+celsius(t))
		}
		for _, k := range s.Store.Keys() {
			if rest, ok := strings.CutPrefix(k, st.Name+"/fs["); ok && strings.HasSuffix(rest, "].used") {
				if u, ok := s.last(k); ok && u > 85 {
					out = append(out, st.Name+" "+strings.TrimSuffix(rest, "].used")+" at "+pct(u))
				}
			}
		}
	}
	linked := map[string]bool{}
	for _, g := range groups {
		for _, it := range g.Items {
			if it.Container != "" {
				linked[it.Container] = true
			}
		}
	}
	for _, r := range rows {
		// A container that served a service, or one that went quiet in the last hour. Older gone
		// containers were most likely removed on purpose; the Apps page still lists them.
		if r.Gone && (linked[r.Name] || r.goneFor < time.Hour) {
			out = append(out, r.Name+" stopped reporting "+r.Since)
		}
	}
	if s.Net != nil {
		errs := s.Net.Errors()
		for _, tg := range s.Net.Ping {
			if e := errs["ping "+tg.Name]; e != "" {
				out = append(out, "No answer from "+tg.Name+": "+e)
			} else if v, ok := s.last(netcheck.Prefix + "ping[" + tg.Name + "].loss"); ok && v >= 100 {
				out = append(out, "No answer from "+tg.Name)
			}
		}
		for _, tg := range s.Net.DNS {
			if v, ok := s.last(netcheck.Prefix + "dns[" + tg.Name + "].fail"); ok && v >= 1 {
				out = append(out, "DNS through "+tg.Name+" fails")
			}
		}
		if rs := s.Net.Results(); len(rs) > 0 && rs[len(rs)-1].Err != "" {
			out = append(out, "Last speed test failed: "+rs[len(rs)-1].Err)
		}
	}
	return out
}

// newest is the container whose first point is the latest, and when that was.
func (s *Server) newest(rows []ctrRow) (string, string) {
	to := s.Store.Slot(s.Now())
	from := to - int64(s.Store.Len()) + 1
	best, bestSlot := "", int64(math.MinInt64)
	for _, r := range rows {
		if r.Gone {
			continue
		}
		for i, v := range s.Store.Range(r.Machine+"/ctr["+r.Name+"].ram", from, to) {
			if v == v {
				if from+int64(i) > bestSlot {
					best, bestSlot = r.Name, from+int64(i)
				}
				break
			}
		}
	}
	if best == "" || bestSlot <= from+1 {
		return best, ""
	}
	return best, ago(s.Now(), s.Store.SlotTime(bestSlot))
}

func (s *Server) arcane(groups []svcGroup) string {
	if s.Arcane != "" {
		return s.Arcane
	}
	for _, g := range groups {
		for _, it := range g.Items {
			if strings.EqualFold(it.Name, "arcane") {
				return it.URL
			}
		}
	}
	return ""
}

// --- memory ---

type part struct {
	Name, Size, Fill string
	W                float64
}

type budget struct {
	Total string
	Parts []part
}

// budget splits the host's RAM into the VMs' sizes, the host's own use and what is free.
func (s *Server) budget() *budget {
	total, ok1 := s.last(s.Host + "/ram.total")
	used, ok2 := s.last(s.Host + "/ram.used")
	if !ok1 || !ok2 || total <= 0 {
		return nil
	}
	b := &budget{Total: bytes(total)}
	vms := 0.0
	i := 0
	for _, st := range s.Statuses() {
		if st.Name == s.Host {
			continue
		}
		if v, ok := s.last(st.Name + "/ram.total"); ok {
			b.Parts = append(b.Parts, part{Name: st.Name, Size: bytes(v), Fill: []string{"var(--po-sky)", "var(--po-mustard)", "var(--po-lavender)"}[i%3], W: v})
			vms += v
			i++
		}
	}
	other := math.Max(0, used-vms)
	free := math.Max(0, total-math.Max(used, vms))
	b.Parts = append(b.Parts, part{Name: "host itself", Size: bytes(other), Fill: "var(--po-coral)", W: other}, part{Name: "free", Size: bytes(free), Fill: "var(--po-card)", W: free})
	for j := range b.Parts {
		b.Parts[j].W = math.Round(1000*b.Parts[j].W/total) / 10
	}
	return b
}

// --- network card ---

type netHome struct {
	*netCard
	Big, BigUnit string
	Kind, State  string
	Lines        []string
	Spark        columns
}

func (s *Server) netHome(rg Range, from, to int64) *netHome {
	c := s.netCard()
	if c == nil {
		return nil
	}
	h := &netHome{netCard: c, Big: "-", Kind: "ok", State: "Online"}
	for i, tg := range s.Net.Ping {
		k := netcheck.Prefix + "ping[" + tg.Name + "]"
		rtt, ok := s.last(k + ".rtt")
		loss, _ := s.last(k + ".loss")
		line := tg.Name + " "
		if ok {
			line += msec(rtt)
		} else {
			line += "no answer"
		}
		line += ", loss " + pct(loss)
		h.Lines = append(h.Lines, line)
		switch {
		case loss >= 100 || !ok:
			h.Kind, h.State = "bad", "Down"
		case loss > 0 && h.Kind == "ok":
			h.Kind, h.State = "warn", "Losing"
		}
		if i == 0 {
			if ok {
				h.Big, h.BigUnit = splitUnit(msec(rtt))
			}
			h.BigUnit += " to " + tg.Name
			h.Spark = s.cols(s.Store.Range(k+".rtt", from, to), from, rg, 12, 0, msec, "", "")
		}
	}
	return h
}

// --- rank ---

type rankItem struct {
	Name, RAM, Fill string
	H               float64
	N               int
}

func rank(rows []ctrRow) []rankItem {
	var out []rankItem
	max := 0.0
	for _, r := range rows {
		if r.Gone {
			continue
		}
		if max == 0 {
			max = r.ramBytes
		}
		out = append(out, rankItem{Name: r.Name, RAM: r.RAM, H: math.Max(18, r.ramBytes/max*66), N: len(out) + 1, Fill: fill(len(out))})
		if len(out) == 6 {
			break
		}
	}
	return out
}

// --- pages ---

type homeBody struct {
	Hero     hero
	Groups   []svcGroup
	SvcErr   string
	Machines []mcard
	Budget   *budget
	Rank     []rankItem
	Ctrs     int
	Net      *netHome
	VM       *mcard
	VMCols   columns
	HasSvc   bool
}

func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	p, rg, from, to := s.base(r, "Home", "home")
	groups, up, checked, svcErr := s.groups(true)
	rows := s.ctrRows(rg, from, to)
	b := homeBody{Groups: groups, Machines: s.mcards(rg, from, to, 0), Budget: s.budget(), Rank: rank(rows), Net: s.netHome(rg, from, to), HasSvc: s.Services != nil}
	if svcErr != nil {
		b.SvcErr = svcErr.Error()
	}
	live := 0
	for _, r := range rows {
		if !r.Gone {
			live++
		}
	}
	b.Ctrs = live
	probs := s.problems(groups, svcErr, rows)
	b.Hero = hero{AppsUp: up, AppsTotal: checked, Containers: live, Arcane: s.arcane(groups)}
	if len(probs) > 5 {
		b.Hero.Problems, b.Hero.More = probs[:5], len(probs)-5
	} else {
		b.Hero.Problems = probs
	}
	b.Hero.Newest, b.Hero.NewestAgo = s.newest(rows)
	for i := range b.Machines {
		if b.Machines[i].Name == s.Local {
			vm := b.Machines[i]
			b.VM = &vm
			b.VMCols = s.cols(s.Store.Range(s.Local+"/cpu", from, to), from, rg, 12, 100, pct, "0 %", "100 %")
		}
	}
	p.Body = b
	s.render(w, "home.html", p)
}

type appsBody struct {
	Groups []svcGroup
	SvcErr string
	HasSvc bool
	Rows   []ctrRow
	Live   int
}

func (s *Server) apps(w http.ResponseWriter, r *http.Request) {
	p, rg, from, to := s.base(r, "Apps", "apps")
	p.Lead = "Every service and every container. CPU is the share of the whole machine; RAM is biggest first."
	groups, _, _, svcErr := s.groups(true)
	b := appsBody{Groups: groups, HasSvc: s.Services != nil, Rows: s.ctrRows(rg, from, to)}
	if svcErr != nil {
		b.SvcErr = svcErr.Error()
	}
	byCtr := map[string]svcItem{}
	for _, g := range groups {
		for _, it := range g.Items {
			if it.Container != "" {
				byCtr[it.Container] = it
			}
		}
	}
	for i := range b.Rows {
		if it, ok := byCtr[b.Rows[i].Name]; ok {
			b.Rows[i].Service, b.Rows[i].URL = it.Name, it.URL
		}
		if !b.Rows[i].Gone {
			b.Live++
		}
	}
	p.Body = b
	s.render(w, "apps.html", p)
}

func (s *Server) machines(w http.ResponseWriter, r *http.Request) {
	p, rg, from, to := s.base(r, "Machines", "machines")
	p.Lead = "Every machine, one point every " + every(s.Store.Interval()) + ", kept " + keepLabel(s.Store) + ". Columns show the peak of each part of the range."
	p.Body = s.mcards(rg, from, to, 24)
	s.render(w, "machines.html", p)
}

// --- login ---

type loginBody struct {
	Groups []svcGroup
	Next   string
	Error  string
}

func (s *Server) loginPage(w http.ResponseWriter, r *http.Request, next, msg string, code int) {
	groups, _, _, _ := s.groups(false)
	p := page{Title: "Log in", Version: s.Version, Path: "/", Range: ranges[2], Clock: s.Now().Format("15:04"), Date: s.Now().Format("Mon 2 Jan"),
		Body: loginBody{Groups: groups, Next: auth.Next(next), Error: msg}}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	if err := s.tmpl.ExecuteTemplate(w, "login.html", p); err != nil {
		s.Log.Error("render", "page", "login", "err", err)
	}
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if s.Auth == nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := r.ParseForm(); err != nil || !auth.SameOrigin(r) {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	next := auth.Next(r.PostFormValue("next"))
	switch err := s.Auth.Check(auth.ClientIP(r), r.PostFormValue("password")); {
	case err == nil:
		s.Auth.Start(w, r)
		http.Redirect(w, r, next, http.StatusSeeOther)
	case errors.Is(err, auth.ErrWait):
		s.loginPage(w, r, next, "Too many wrong passwords. Wait a minute, then try again.", http.StatusTooManyRequests)
	default:
		s.loginPage(w, r, next, "Wrong password.", http.StatusUnauthorized)
	}
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if s.Auth != nil {
		if !auth.SameOrigin(r) {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		s.Auth.End(w, r)
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
