package web

import (
	"fmt"
	"math"
	"time"
)

// every names an interval in words: "15 s", "75 s", "5 min", "34 min".
func every(d time.Duration) string {
	if d < 2*time.Minute {
		return fmt.Sprintf("%d s", int(d.Seconds()))
	}
	return fmt.Sprintf("%.0f min", d.Minutes())
}

func pct(v float64) string {
	if v < 10 {
		return fmt.Sprintf("%.1f %%", v)
	}
	return fmt.Sprintf("%.0f %%", v)
}

// bytes uses binary units, like docker stats and free.
func bytes(v float64) string {
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	i := 0
	for math.Abs(v) >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	switch {
	case i == 0:
		return fmt.Sprintf("%.0f B", v)
	case v < 10:
		return fmt.Sprintf("%.2f %s", v, units[i])
	case v < 100:
		return fmt.Sprintf("%.1f %s", v, units[i])
	}
	return fmt.Sprintf("%.0f %s", v, units[i])
}

func rate(v float64) string { return bytes(v) + "/s" }

func load(v float64) string { return fmt.Sprintf("%.2f", v) }

func celsius(v float64) string { return fmt.Sprintf("%.0f °C", v) }

func mhz(v float64) string { return fmt.Sprintf("%.0f MHz", v) }

// ago says how long ago t was, in the largest whole unit.
func ago(now, t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%d s ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh %02dm ago", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%d days ago", int(d.Hours()/24))
}

func uptime(sec float64) string {
	d := time.Duration(sec) * time.Second
	if d >= 48*time.Hour {
		return fmt.Sprintf("%d days", int(d.Hours()/24))
	}
	return fmt.Sprintf("%dh %02dm", int(d.Hours()), int(d.Minutes())%60)
}
