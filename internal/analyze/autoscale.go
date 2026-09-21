package analyze

import (
	"math"
	"regexp"
	"strconv"
	"strings"
)

// ScalingChange is one vertex decision from an operator ScalingReport event.
type ScalingChange struct {
	VID      string
	From, To int
	Applied  bool // false when the autoscaler only recommended (scaling disabled)
}

// scalingLine matches the per-vertex part of the operator's ScalingReport
// message, e.g. "Vertex ID 6d26…f4 | Parallelism 1 -> 2 | Processing capacity …".
// The surrounding wording changed across operator versions; this part didn't.
var scalingLine = regexp.MustCompile(`Vertex ID ([0-9a-fA-F]{32})\s*\|\s*Parallelism (\d+)\s*->\s*(\d+)`)

// ParseScalingReport extracts the per-vertex parallelism changes from one
// ScalingReport event message. Applied reflects whether the operator said it
// is executing the scaling or merely recommending it.
func ParseScalingReport(msg string) []ScalingChange {
	applied := !strings.Contains(strings.ToLower(msg), "recommend")
	var out []ScalingChange
	for _, m := range scalingLine.FindAllStringSubmatch(msg, -1) {
		from, _ := strconv.Atoi(m[2])
		to, _ := strconv.Atoi(m[3])
		out = append(out, ScalingChange{VID: strings.ToLower(m[1]), From: from, To: to, Applied: applied})
	}
	return out
}

// Autoscaler defaults, as documented by the Flink Kubernetes Operator.
const (
	DefaultTargetUtilization = 0.7
	DefaultUtilizationBound  = 0.3
)

// EstimateParallelism is f9s's utilization-based parallelism estimate: the
// parallelism at which the vertex's average busy fraction would sit at the
// target utilization. It returns current while utilization is inside
// target ± boundary (the autoscaler's own dead band), so steady jobs don't
// get a stream of ±1 suggestions. For a saturated vertex the result is a
// lower bound. maxPar <= 0 means unbounded.
func EstimateParallelism(current int, busyFrac, target, boundary float64, maxPar int) int {
	if current <= 0 || busyFrac < 0 || target <= 0 {
		return current
	}
	if boundary < 0 {
		boundary = 0
	}
	// busy time saturates at 100%, so a pegged vertex hides how much work
	// is waiting: ≥95% counts as over the band even when the band reaches 1.0
	saturated := busyFrac >= 0.95
	if !saturated && busyFrac >= target-boundary && busyFrac <= target+boundary {
		return current
	}
	est := int(math.Ceil(float64(current) * busyFrac / target))
	if est < 1 {
		est = 1
	}
	if maxPar > 0 && est > maxPar {
		est = maxPar
	}
	return est
}

// ConfigFloat reads a float autoscaler setting from flattened CR config,
// accepting both key prefixes the operator has used; def when absent/invalid.
func ConfigFloat(conf map[string]string, suffix string, def float64) float64 {
	for _, k := range []string{"job.autoscaler." + suffix, "kubernetes.operator.job.autoscaler." + suffix} {
		if v, ok := conf[k]; ok {
			if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
				return f
			}
		}
	}
	return def
}
