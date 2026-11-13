package web

import (
	"fmt"
	"html/template"
	"net/http"
	"time"

	"doinspect/server/internal/config"
	"doinspect/server/internal/netcheck"
)

// Network is what the network page reads (F22). A nil Network leaves the page and the overview
// card out.
type Network struct {
	Ping, DNS []config.Target
	Results   func() []netcheck.Result
	Running   func() bool
	Next      func() time.Time // zero when no speed test is set up
	Errors    func() map[string]string
	CPUOf     string // machine whose CPU peak shows next to each test
	DNSName   string // the name the DNS checks look up
	Keep      time.Duration
}

func mbit(bps float64) string {
	v := bps / 1e6
	if v < 100 {
		return fmt.Sprintf("%.1f Mbit/s", v)
	}
	return fmt.Sprintf("%.0f Mbit/s", v)
}

func msec(v float64) string {
	if v < 10 {
		return fmt.Sprintf("%.1f ms", v)
	}
	return fmt.Sprintf("%.0f ms", v)
}

// netCard is the overview's network card.
type netCard struct {
	ISP, Down, Up, When, GatewayRTT, Err string
	Has                                  bool
}

func (s *Server) netCard() *netCard {
	if s.Net == nil {
		return nil
	}
	c := &netCard{}
	if rs := s.Net.Results(); len(rs) > 0 {
		r := rs[len(rs)-1]
		c.Has, c.ISP, c.When, c.Err = true, r.ISP, ago(s.Now(), r.End), r.Err
		c.Down, c.Up = mbit(r.Down), mbit(r.Up)
	}
	if len(s.Net.Ping) > 0 {
		if v, ok := s.last(netcheck.Prefix + "ping[" + s.Net.Ping[0].Name + "].rtt"); ok {
			c.GatewayRTT = s.Net.Ping[0].Name + " " + msec(v)
		}
	}
	return c
}

type pingCard struct {
	Name, Addr, Now, Err  string
	RTT, Jitter, Loss     template.HTML
	RTTPeak, JitNow, LNow string
	Fails                 string // DNS: "0 of 96 (0.0 %)" over the range
}

type speedRow struct {
	When, Down, Up, Idle, Loaded, CPU, ISP, IP, Site, Err string
	Changed                                               bool
}

type networkBody struct {
	ISP, ASN, IP, Site, ISPSince string
	Latest                       *speedRow
	Running                      bool
	Next                         string
	Pings, DNS                   []pingCard
	Tests                        []speedRow
	DownChart, UpChart           template.HTML
	Keep                         string
	Since                        string // when the checks started, if that is inside the range
	Every, DNSName               string
}

func (s *Server) network(w http.ResponseWriter, r *http.Request) {
	if s.Net == nil {
		http.NotFound(w, r)
		return
	}
	p, rg, from, to := s.base(r, "Network", "network")
	p.Lead = "Ping, jitter and loss every " + every(s.Store.Interval()) + ", DNS lookups, and the speed test."
	errs := s.Net.Errors()
	b := networkBody{Running: s.Net.Running(), Keep: itoa(int(s.Net.Keep.Hours()/24)) + " days", Every: every(s.Store.Interval()), DNSName: s.Net.DNSName}
	if n := s.Net.Next(); !n.IsZero() {
		b.Next = n.Format("Mon 15:04")
	}
	big := func(key string, o chartOpts) template.HTML {
		o.W, o.H, o.Ticks = 520, 160, rg.Ticks
		return svgChart(s.series(key, from, to), o)
	}
	small := func(key string, o chartOpts) template.HTML {
		o.W, o.H, o.Small = 300, 50, true
		return svgChart(s.series(key, from, to), o)
	}
	nowOf := func(key string, f func(float64) string) string {
		if v, ok := s.last(key); ok {
			return f(v)
		}
		return "-"
	}
	for _, tg := range s.Net.Ping {
		k := netcheck.Prefix + "ping[" + tg.Name + "]"
		c := pingCard{Name: tg.Name, Addr: tg.URL, Err: errs["ping "+tg.Name],
			RTT:    big(k+".rtt", chartOpts{Format: msec}),
			Jitter: small(k+".jitter", chartOpts{}),
			Loss:   small(k+".loss", chartOpts{Max: 100}),
			Now:    nowOf(k+".rtt", msec), JitNow: nowOf(k+".jitter", msec), LNow: nowOf(k+".loss", pct)}
		if v, ok := peak(s.series(k+".rtt", from, to)); ok {
			c.RTTPeak = "peak " + msec(v)
		}
		b.Pings = append(b.Pings, c)
	}
	for _, tg := range s.Net.DNS {
		k := netcheck.Prefix + "dns[" + tg.Name + "]"
		c := pingCard{Name: tg.Name, Addr: tg.URL, Err: errs["dns "+tg.Name],
			RTT: big(k+".ms", chartOpts{Format: msec}),
			Now: nowOf(k+".ms", msec)}
		if v, ok := peak(s.series(k+".ms", from, to)); ok {
			c.RTTPeak = "peak " + msec(v)
		}
		// Failures in the range, as a share of lookups.
		fails, n := 0.0, 0
		for _, v := range s.Store.Range(k+".fail", from, to) {
			if v == v {
				fails += float64(v)
				n++
			}
		}
		if n > 0 {
			c.Fails = itoa(int(fails)) + " of " + itoa(n) + " (" + pct(100*fails/float64(n)) + ")"
		}
		b.DNS = append(b.DNS, c)
	}

	b.Since = s.netSince(from, to)

	rs := s.Net.Results()
	var down, up []Bucket
	for i, r := range rs {
		row := speedRow{When: r.Start.In(s.Now().Location()).Format("Mon 02 Jan 15:04"), ISP: r.ISP, IP: r.IP, Site: r.Site, Err: r.Err,
			Down: mbit(r.Down), Up: mbit(r.Up), Idle: "-", Loaded: "-", CPU: "-"}
		if r.Idle > 0 {
			row.Idle = msec(r.Idle)
		}
		if r.Loaded > 0 {
			row.Loaded = msec(r.Loaded)
		}
		if i > 0 && r.IP != "" && rs[i-1].IP != "" && (rs[i-1].ASN != r.ASN || rs[i-1].IP != r.IP) {
			row.Changed = true
		}
		if s.Net.CPUOf != "" {
			if v, ok := peak(s.seriesRaw(s.Net.CPUOf+"/cpu", s.Store.Slot(r.Start), s.Store.Slot(r.End)+1)); ok {
				row.CPU = pct(v)
			}
		}
		b.Tests = append(b.Tests, row)
		down = append(down, Bucket{Avg: r.Down / 1e6, Peak: r.Down / 1e6, OK: r.Down > 0})
		up = append(up, Bucket{Avg: r.Up / 1e6, Peak: r.Up / 1e6, OK: r.Up > 0})
	}
	// Newest first in the table.
	for i, j := 0, len(b.Tests)-1; i < j; i, j = i+1, j-1 {
		b.Tests[i], b.Tests[j] = b.Tests[j], b.Tests[i]
	}
	mb := func(v float64) string { return fmt.Sprintf("%.0f Mbit/s", v) }
	if len(rs) > 0 {
		last := rs[len(rs)-1]
		b.Latest = &b.Tests[0]
		b.ISP, b.IP, b.Site = last.ISP, last.IP, last.Site
		if last.ASN > 0 {
			b.ASN = "AS" + itoa(last.ASN)
		}
		since := last.Start
		for i := len(rs) - 2; i >= 0 && rs[i].ASN == last.ASN && rs[i].IP == last.IP; i-- {
			since = rs[i].Start
		}
		b.ISPSince = since.In(s.Now().Location()).Format("02 Jan 15:04")
		ticks := []string{"oldest", "newest"}
		b.DownChart = svgChart(down, chartOpts{W: 520, H: 140, Format: mb, Ticks: ticks})
		b.UpChart = svgChart(up, chartOpts{W: 520, H: 140, Format: mb, Ticks: ticks})
	}
	p.Body = b
	s.render(w, "network.html", p)
}

// seriesRaw is the stored points of key from from to to as buckets of one point each.
func (s *Server) seriesRaw(key string, from, to int64) []Bucket {
	bs, _ := downsample(s.Store.Range(key, from, to), int(to-from+1))
	return bs
}

// netSince is the time of the first network point in the range, as "Sun 11:36", when the range
// starts before it: the checks started later, so the charts fill only their right part.
func (s *Server) netSince(from, to int64) string {
	first := int64(-1)
	keys := []string{}
	for _, tg := range s.Net.Ping {
		keys = append(keys, netcheck.Prefix+"ping["+tg.Name+"].loss")
	}
	for _, tg := range s.Net.DNS {
		keys = append(keys, netcheck.Prefix+"dns["+tg.Name+"].fail")
	}
	for _, k := range keys {
		for i, v := range s.Store.Range(k, from, to) {
			if v == v {
				if first < 0 || from+int64(i) < first {
					first = from + int64(i)
				}
				break
			}
		}
	}
	if first <= from+1 {
		return ""
	}
	return s.Store.SlotTime(first).In(s.Now().Location()).Format("Mon 15:04")
}
