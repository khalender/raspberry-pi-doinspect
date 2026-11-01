package web

import (
	"fmt"
	"html/template"
	"math"
	"strings"
)

// Bucket is one drawn point: the average of the stored points in its step, and their peak. OK is
// false when the step holds no point at all.
type Bucket struct {
	Avg, Peak float64
	OK        bool
}

// downsample folds vals into at most max buckets of equal steps, skipping NaN points, so a 7-day
// range of 40,320 points draws as about 300 and a short spike still shows as the step's peak.
func downsample(vals []float32, max int) ([]Bucket, int) {
	step := (len(vals) + max - 1) / max
	if step < 1 {
		step = 1
	}
	out := make([]Bucket, 0, (len(vals)+step-1)/step)
	for i := 0; i < len(vals); i += step {
		var b Bucket
		var sum float64
		n := 0
		for _, v := range vals[i:min(i+step, len(vals))] {
			f := float64(v)
			if math.IsNaN(f) {
				continue
			}
			sum += f
			if n == 0 || f > b.Peak {
				b.Peak = f
			}
			n++
		}
		if n > 0 {
			b.Avg, b.OK = sum/float64(n), true
		}
		out = append(out, b)
	}
	return out, step
}

// chartOpts sets up one chart. A small chart has no axes; Max 0 picks a round maximum above the
// highest peak; Limit draws a dashed line, such as the temperature where the Pi slows down.
type chartOpts struct {
	W, H       int
	Max        float64
	Format     func(float64) string
	Limit      float64
	LimitLabel string
	Ticks      []string
	Small      bool
}

// svgChart draws the buckets as an average line over a light area, with a band up to each step's
// peak. A step without points breaks the line, so a gap in the data shows as a gap.
func svgChart(bs []Bucket, o chartOpts) template.HTML {
	pl, pr, pt, pb := 72.0, 8.0, 8.0, 22.0
	if o.Small {
		pl, pr, pt, pb = 2, 6, 4, 4
	}
	w, h := float64(o.W), float64(o.H)
	iw, ih := w-pl-pr, h-pt-pb
	top := o.Max
	if top <= 0 {
		top = niceMax(bs, o.Limit)
	}
	x := func(i int) float64 {
		if len(bs) < 2 {
			return pl + iw
		}
		return pl + float64(i)/float64(len(bs)-1)*iw
	}
	y := func(v float64) float64 { return pt + ih - math.Min(math.Max(v/top, 0), 1)*ih }

	var sb strings.Builder
	fmt.Fprintf(&sb, `<svg viewBox="0 0 %d %d" class="chart%s" role="img" aria-label="chart">`, o.W, o.H, map[bool]string{true: " small"}[o.Small])
	if !o.Small {
		for k := 0; k <= 4; k++ {
			v := top / 4 * float64(k)
			fmt.Fprintf(&sb, `<line class="grid" x1="%.1f" x2="%.1f" y1="%.1f" y2="%.1f"/>`, pl, w-pr, y(v), y(v))
			if k%2 == 0 {
				fmt.Fprintf(&sb, `<text x="%.1f" y="%.1f" text-anchor="end">%s</text>`, pl-6, y(v)+4, template.HTMLEscapeString(o.Format(v)))
			}
		}
		for k, t := range o.Ticks {
			anchor := "middle"
			if k == 0 {
				anchor = "start"
			} else if k == len(o.Ticks)-1 {
				anchor = "end"
			}
			fmt.Fprintf(&sb, `<text x="%.1f" y="%.1f" text-anchor="%s">%s</text>`, pl+float64(k)/float64(len(o.Ticks)-1)*iw, h-6, anchor, t)
		}
	}
	if o.Limit > 0 && o.Limit <= top {
		fmt.Fprintf(&sb, `<line class="limit" x1="%.1f" x2="%.1f" y1="%.1f" y2="%.1f"/>`, pl, w-pr, y(o.Limit), y(o.Limit))
		if !o.Small && o.LimitLabel != "" {
			fmt.Fprintf(&sb, `<text class="limit" x="%.1f" y="%.1f" text-anchor="end">%s</text>`, w-pr, y(o.Limit)-4, template.HTMLEscapeString(o.LimitLabel))
		}
	}
	// One path per run of steps with data.
	var line, band, area strings.Builder
	for i := 0; i < len(bs); {
		if !bs[i].OK {
			i++
			continue
		}
		j := i
		for j < len(bs) && bs[j].OK {
			j++
		}
		run := bs[i:j]
		for k, b := range run {
			cmd := "L"
			if k == 0 {
				cmd = "M"
			}
			fmt.Fprintf(&line, "%s%.1f,%.1f", cmd, x(i+k), y(b.Avg))
			fmt.Fprintf(&band, "%s%.1f,%.1f", cmd, x(i+k), y(b.Peak))
		}
		for k := len(run) - 1; k >= 0; k-- {
			fmt.Fprintf(&band, "L%.1f,%.1f", x(i+k), y(run[k].Avg))
		}
		band.WriteString("Z")
		fmt.Fprintf(&area, "M%.1f,%.1f", x(i), pt+ih)
		for k, b := range run {
			fmt.Fprintf(&area, "L%.1f,%.1f", x(i+k), y(b.Avg))
		}
		fmt.Fprintf(&area, "L%.1f,%.1fZ", x(j-1), pt+ih)
		i = j
	}
	if band.Len() > 0 {
		fmt.Fprintf(&sb, `<path class="band" d="%s"/><path class="area" d="%s"/><path class="line" d="%s"/>`, band.String(), area.String(), line.String())
	}
	if n := len(bs); n > 0 && bs[n-1].OK {
		fmt.Fprintf(&sb, `<circle class="dot" cx="%.1f" cy="%.1f" r="%.1f"/>`, x(n-1), y(bs[n-1].Avg), map[bool]float64{true: 2.5, false: 3.5}[o.Small])
	}
	sb.WriteString(`</svg>`)
	return template.HTML(sb.String())
}

// niceMax is the smallest 1, 2 or 5 times a power of ten at or above the highest peak and the limit.
func niceMax(bs []Bucket, limit float64) float64 {
	hi := limit
	for _, b := range bs {
		if b.OK && b.Peak > hi {
			hi = b.Peak
		}
	}
	if hi <= 0 {
		return 1
	}
	p := math.Pow(10, math.Floor(math.Log10(hi)))
	for _, m := range []float64{1, 2, 5, 10} {
		if m*p >= hi {
			return m * p
		}
	}
	return 10 * p
}

func peak(bs []Bucket) (float64, bool) {
	hi, ok := 0.0, false
	for _, b := range bs {
		if b.OK && (!ok || b.Peak > hi) {
			hi, ok = b.Peak, true
		}
	}
	return hi, ok
}
