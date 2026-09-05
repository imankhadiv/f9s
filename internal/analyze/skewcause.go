package analyze

// SkewCause classifies why a vertex's load is uneven — the fix differs
// per cause, so naming it is half the remedy.
type SkewCause string

// The causes the classifier can distinguish from REST-observable data.
const (
	CauseNone               SkewCause = ""
	CausePartitionImbalance SkewCause = "partition/assignment imbalance"
	CauseHotKey             SkewCause = "hot key"
	CauseExpensiveKey       SkewCause = "expensive keys"
)

// SkewDiagnosis is the classifier's verdict with a cause-mapped remedy.
type SkewDiagnosis struct {
	Cause  SkewCause
	Detail string
	Fix    string
}

// ClassifySkew walks the decision tree over per-subtask record rates and
// busy times. isSource marks source vertices (their skew comes from the
// outside world, not from keyBy).
func ClassifySkew(isSource bool, recs, busy []float64) SkewDiagnosis {
	recSkew := SkewScore(recs)
	busySkew := SkewScore(busy)

	if isSource && recSkew >= 30 {
		return SkewDiagnosis{
			Cause:  CausePartitionImbalance,
			Detail: "a source subtask receives an uneven share — partition assignment or producer keying, not Flink keys",
			Fix:    "make source parallelism a divisor of the partition count (ideally equal); check producer partitioning; set withIdleness() so empty subtasks don't stall watermarks",
		}
	}
	if !isSource && recSkew >= 30 {
		return SkewDiagnosis{
			Cause:  CauseHotKey,
			Detail: "few keys dominate the keyBy/GROUP BY — record counts are uneven across subtasks",
			Fix:    "DataStream: salt the key + two-phase aggregate. SQL: table.exec.mini-batch.enabled=true + table.optimizer.agg-phase-strategy=TWO_PHASE (+ distinct-agg.split.enabled for COUNT(DISTINCT))",
		}
	}
	if !isSource && recSkew < 30 && busySkew >= 30 {
		return SkewDiagnosis{
			Cause:  CauseExpensiveKey,
			Detail: "record counts are even but busy time is not — some keys cost more CPU than others",
			Fix:    "profile the hot subtask (flame graph); consider splitting the expensive computation or keying by an additional dimension",
		}
	}
	return SkewDiagnosis{Cause: CauseNone}
}
