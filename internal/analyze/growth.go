package analyze

import (
	"fmt"
	"math"
	"time"
)

// GrowthPoint is one state-size observation.
type GrowthPoint struct {
	T    time.Time
	Size int64 // bytes
}

// GrowthVerdict is the state-growth analysis for one job.
type GrowthVerdict struct {
	BytesPerDay float64 // least-squares slope, bytes/day (can be negative)
	Latest      int64
	Trend       string // "stable" | "growing" | "shrinking"
	// EtaDays is days until a configured volume fills at the current rate;
	// <=0 means no growth or no limit given.
	EtaDays float64
	EtaDate time.Time
}

// StateGrowth fits a line to (time, size) points and reports the daily
// growth rate. With volumeBytes > 0 and positive growth, it also projects
// when the volume fills. Fewer than 3 points → stable, no projection.
func StateGrowth(points []GrowthPoint, volumeBytes int64) GrowthVerdict {
	v := GrowthVerdict{Trend: "stable"}
	if len(points) == 0 {
		return v
	}
	v.Latest = points[len(points)-1].Size
	if len(points) < 3 {
		return v
	}
	// least squares with x in days relative to the first point
	x0 := points[0].T
	var sx, sy, sxx, sxy float64
	n := float64(len(points))
	for _, p := range points {
		x := p.T.Sub(x0).Hours() / 24
		y := float64(p.Size)
		sx += x
		sy += y
		sxx += x * x
		sxy += x * y
	}
	denom := n*sxx - sx*sx
	if denom == 0 {
		return v
	}
	slope := (n*sxy - sx*sy) / denom // bytes per day
	v.BytesPerDay = slope
	// relative threshold: >2% of latest per day (and >1MB/day absolute) to
	// call it growth — avoids flagging sampling jitter on tiny state.
	rel := 0.0
	if v.Latest > 0 {
		rel = slope / float64(v.Latest)
	}
	switch {
	case slope >= 1<<20 && rel >= 0.02:
		v.Trend = "growing"
	case slope <= -(1<<20) && rel <= -0.02:
		v.Trend = "shrinking"
	}
	if v.Trend == "growing" && volumeBytes > 0 && volumeBytes > v.Latest {
		remaining := float64(volumeBytes - v.Latest)
		v.EtaDays = remaining / slope
		v.EtaDate = time.Now().Add(time.Duration(v.EtaDays * 24 * float64(time.Hour)))
	}
	return v
}

// Summary renders the verdict as a one-liner for the checkpoints view.
func (v GrowthVerdict) Summary() string {
	switch v.Trend {
	case "growing":
		s := fmt.Sprintf("state growing %s/day", fmtBytesPerDay(v.BytesPerDay))
		if v.EtaDays > 0 {
			s += fmt.Sprintf(" — disk full in ~%s (%s)", fmtDays(v.EtaDays), v.EtaDate.Format("Jan 2"))
		} else {
			s += " — set stateVolumeGB for a disk-full ETA"
		}
		return s
	case "shrinking":
		return fmt.Sprintf("state shrinking %s/day", fmtBytesPerDay(-v.BytesPerDay))
	default:
		return "state stable"
	}
}

func fmtBytesPerDay(b float64) string {
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	i := 0
	for b >= 1024 && i < len(units)-1 {
		b /= 1024
		i++
	}
	return fmt.Sprintf("%.1f%s", b, units[i])
}

func fmtDays(d float64) string {
	if d >= 1 {
		return fmt.Sprintf("%.0f days", math.Round(d))
	}
	return fmt.Sprintf("%.0f hours", math.Round(d*24))
}
