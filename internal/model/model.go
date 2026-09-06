// Package model holds typed views of Flink's monitoring REST API responses
// plus the formatting helpers that turn them into table cells.
package model

import (
	"fmt"
	"strconv"
	"time"
)

// ClusterOverview is GET /overview.
type ClusterOverview struct {
	TaskManagers   int    `json:"taskmanagers"`
	SlotsTotal     int    `json:"slots-total"`
	SlotsAvailable int    `json:"slots-available"`
	JobsRunning    int    `json:"jobs-running"`
	JobsFinished   int    `json:"jobs-finished"`
	JobsCancelled  int    `json:"jobs-cancelled"`
	JobsFailed     int    `json:"jobs-failed"`
	FlinkVersion   string `json:"flink-version"`
}

// TaskCounts appears on jobs and vertices.
type TaskCounts struct {
	Total   int `json:"total"`
	Running int `json:"running"`
	Failed  int `json:"failed"`
}

// JobOverview is one entry of GET /jobs/overview.
type JobOverview struct {
	JID       string     `json:"jid"`
	Name      string     `json:"name"`
	State     string     `json:"state"`
	StartTime int64      `json:"start-time"`
	Duration  int64      `json:"duration"`
	Tasks     TaskCounts `json:"tasks"`
}

// JobsOverview is GET /jobs/overview.
type JobsOverview struct {
	Jobs []JobOverview `json:"jobs"`
}

// VertexMetrics is the per-vertex IO block on GET /jobs/:jid.
type VertexMetrics struct {
	ReadBytes    int64 `json:"read-bytes"`
	WriteBytes   int64 `json:"write-bytes"`
	ReadRecords  int64 `json:"read-records"`
	WriteRecords int64 `json:"write-records"`
}

// Vertex is one job vertex on GET /jobs/:jid.
type Vertex struct {
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	Parallelism int           `json:"parallelism"`
	Status      string        `json:"status"`
	Metrics     VertexMetrics `json:"metrics"`
}

// PlanInput is one upstream edge of a plan node. ShipStrategy is the shuffle
// mode Flink prints on the edge (FORWARD / HASH / REBALANCE / …).
type PlanInput struct {
	ID           string `json:"id"`
	ShipStrategy string `json:"ship_strategy"`
	Exchange     string `json:"exchange"`
}

// PlanNode is one vertex in GET /jobs/:jid/plan.
type PlanNode struct {
	ID          string      `json:"id"`
	Description string      `json:"description"`
	Parallelism int         `json:"parallelism"`
	Inputs      []PlanInput `json:"inputs"`
}

// Plan is the execution graph topology.
type Plan struct {
	Nodes []PlanNode `json:"nodes"`
}

// Job is GET /jobs/:jid.
type Job struct {
	JID          string         `json:"jid"`
	Name         string         `json:"name"`
	State        string         `json:"state"`
	StartTime    int64          `json:"start-time"`
	Duration     int64          `json:"duration"`
	Vertices     []Vertex       `json:"vertices"`
	StatusCounts map[string]int `json:"status-counts"`
}

// Backpressure is GET /jobs/:jid/vertices/:vid/backpressure.
// Older Flink emits "backpressure-level", newer "backpressureLevel".
type Backpressure struct {
	Status     string `json:"status"`
	LevelKebab string `json:"backpressure-level"`
	LevelCamel string `json:"backpressureLevel"`
	Subtasks   []struct {
		Ratio float64 `json:"ratio"`
	} `json:"subtasks"`
}

// Level normalizes the two spellings; empty means unknown/not sampled yet.
func (b Backpressure) Level() string {
	if b.LevelCamel != "" {
		return b.LevelCamel
	}
	return b.LevelKebab
}

// MaxRatioPercent is the worst subtask's backpressure ratio as a percentage,
// or -1 when the cluster doesn't report ratios.
func (b Backpressure) MaxRatioPercent() int {
	if len(b.Subtasks) == 0 {
		return -1
	}
	maxRatio := 0.0
	for _, s := range b.Subtasks {
		if s.Ratio > maxRatio {
			maxRatio = s.Ratio
		}
	}
	return int(maxRatio * 100)
}

// CheckpointCounts is the counts block of GET /jobs/:jid/checkpoints.
type CheckpointCounts struct {
	Restored   int `json:"restored"`
	Total      int `json:"total"`
	InProgress int `json:"in_progress"`
	Completed  int `json:"completed"`
	Failed     int `json:"failed"`
}

// Checkpoint is one history entry of GET /jobs/:jid/checkpoints.
type Checkpoint struct {
	ID               int64  `json:"id"`
	Status           string `json:"status"`
	IsSavepoint      bool   `json:"is_savepoint"`
	TriggerTimestamp int64  `json:"trigger_timestamp"`
	StateSize        int64  `json:"state_size"`
	EndToEndDuration int64  `json:"end_to_end_duration"`
	NumSubtasks      int    `json:"num_subtasks"`
	NumAcknowledged  int    `json:"num_acknowledged_subtasks"`
	FailureMessage   string `json:"failure_message"`
	ExternalPath     string `json:"external_path"`
}

// JSONFloat tolerates Flink's habit of serializing NaN/Infinity as strings
// ("p50": "NaN" on young jobs) — those decode as 0 (absent) instead of
// failing the whole response.
type JSONFloat float64

func (f *JSONFloat) UnmarshalJSON(data []byte) error {
	s := string(data)
	if len(s) > 1 && s[0] == '"' {
		s = s[1 : len(s)-1]
	}
	switch s {
	case "NaN", "Infinity", "-Infinity", "":
		*f = 0
		return nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		*f = 0
		return nil // tolerate anything — a stat is never worth a failed parse
	}
	*f = JSONFloat(v)
	return nil
}

// SummaryStat is one percentile block of the checkpoint summary.
type SummaryStat struct {
	Min  JSONFloat `json:"min"`
	Max  JSONFloat `json:"max"`
	Avg  JSONFloat `json:"avg"`
	P50  JSONFloat `json:"p50"`
	P90  JSONFloat `json:"p90"`
	P99  JSONFloat `json:"p99"`
	P999 JSONFloat `json:"p999"`
}

// CheckpointSummary carries the percentile stats the Web UI shows on its
// checkpoints summary tab.
type CheckpointSummary struct {
	StateSize        SummaryStat `json:"state_size"`
	EndToEndDuration SummaryStat `json:"end_to_end_duration"`
	AlignmentBuffer  SummaryStat `json:"alignment_buffered"`
}

// Checkpoints is GET /jobs/:jid/checkpoints.
type Checkpoints struct {
	Counts  CheckpointCounts  `json:"counts"`
	Summary CheckpointSummary `json:"summary"`
	History []Checkpoint      `json:"history"`
}

// JobConfig is GET /jobs/:jid/config.
type JobConfig struct {
	ExecutionConfig struct {
		RestartStrategy string         `json:"restart-strategy"`
		JobParallelism  int            `json:"job-parallelism"`
		ObjectReuse     bool           `json:"object-reuse-mode"`
		UserConfig      map[string]any `json:"user-config"`
	} `json:"execution-config"`
}

// SubtaskInfo is one subtask row of GET /jobs/:jid/vertices/:vid (and the
// per-attempt endpoint, which shares the shape).
type SubtaskInfo struct {
	Subtask        int              `json:"subtask"`
	Status         string           `json:"status"`
	Attempt        int              `json:"attempt"`
	Endpoint       string           `json:"endpoint"`
	StartTime      int64            `json:"start-time"`
	EndTime        int64            `json:"end-time"`
	Duration       int64            `json:"duration"`
	TaskManagerID  string           `json:"taskmanager-id"`
	StatusDuration map[string]int64 `json:"status-duration"`
}

// LogFile is one rotated log file on a JM/TM.
type LogFile struct {
	Name  string `json:"name"`
	Size  int64  `json:"size"`
	Mtime int64  `json:"mtime"`
}

// CheckpointConfig is GET /jobs/:jid/checkpoints/config.
type CheckpointConfig struct {
	Mode              string `json:"mode"` // exactly_once / at_least_once
	Interval          int64  `json:"interval"`
	Timeout           int64  `json:"timeout"`
	UnalignedEnabled  bool   `json:"unaligned_checkpoints"`
	StateBackend      string `json:"state_backend"`
	CheckpointStorage string `json:"checkpoint_storage"`
	ChangelogEnabled  bool   `json:"state_changelog_enabled"`
	ChangelogStorage  string `json:"changelog_storage"`
}

// CheckpointSubtask is one subtask row of
// GET /jobs/:jid/checkpoints/details/:id/subtasks/:vertexid.
type CheckpointSubtask struct {
	Index      int    `json:"index"`
	Status     string `json:"status"`
	AckTS      int64  `json:"ack_timestamp"`
	StateSize  int64  `json:"state_size"`
	E2E        int64  `json:"end_to_end_duration"`
	Checkpoint struct {
		Sync  int64 `json:"sync"`
		Async int64 `json:"async"`
	} `json:"checkpoint"`
	Alignment struct {
		Duration  int64 `json:"duration"`
		Persisted int64 `json:"persisted"`
	} `json:"alignment"`
	StartDelay int64 `json:"start_delay"`
	Unaligned  bool  `json:"unaligned_checkpoint"`
}

// CheckpointDetail is GET /jobs/:jid/checkpoints/details/:id (summary part).
type CheckpointDetail struct {
	ID     int64  `json:"id"`
	Status string `json:"status"`
	Tasks  map[string]struct {
		StateSize int64 `json:"state_size"`
	} `json:"tasks"`
}

// ExceptionEntry is one entry of the exception history.
type ExceptionEntry struct {
	ExceptionName string `json:"exceptionName"`
	Stacktrace    string `json:"stacktrace"`
	Timestamp     int64  `json:"timestamp"`
	TaskName      string `json:"taskName"`
	Location      string `json:"location"`
	// FailureLabels are FLIP-194 enricher classifications (1.17+); empty
	// unless the cluster configures FailureEnrichers.
	FailureLabels map[string]string `json:"failureLabels"`
}

// Exceptions is GET /jobs/:jid/exceptions.
type Exceptions struct {
	RootException string `json:"root-exception"`
	Timestamp     int64  `json:"timestamp"`
	History       struct {
		Entries   []ExceptionEntry `json:"entries"`
		Truncated bool             `json:"truncated"`
	} `json:"exceptionHistory"`
}

// TaskManager is one entry of GET /taskmanagers.
type TaskManager struct {
	ID                     string `json:"id"`
	Path                   string `json:"path"`
	SlotsNumber            int    `json:"slotsNumber"`
	FreeSlots              int    `json:"freeSlots"`
	TimeSinceLastHeartbeat int64  `json:"timeSinceLastHeartbeat"` // epoch ms of last heartbeat
	Hardware               struct {
		CPUCores       float64 `json:"cpuCores"`
		PhysicalMemory int64   `json:"physicalMemory"`
		FreeMemory     int64   `json:"freeMemory"`
		ManagedMemory  int64   `json:"managedMemory"`
	} `json:"hardware"`
}

// TaskManagers is GET /taskmanagers.
type TaskManagers struct {
	TaskManagers []TaskManager `json:"taskmanagers"`
}

// GC is one garbage collector's totals on a TaskManager.
type GC struct {
	Name  string `json:"name"`
	Count int64  `json:"count"`
	Time  int64  `json:"time"`
}

// TMDetail is GET /taskmanagers/:id.
type TMDetail struct {
	TaskManager
	Metrics struct {
		HeapUsed          int64 `json:"heapUsed"`
		HeapCommitted     int64 `json:"heapCommitted"`
		HeapMax           int64 `json:"heapMax"`
		NonHeapUsed       int64 `json:"nonHeapUsed"`
		GarbageCollectors []GC  `json:"garbageCollectors"`
	} `json:"metrics"`
	MemoryConfiguration TMMemoryConfig  `json:"memoryConfiguration"`
	AllocatedSlots      []AllocatedSlot `json:"allocatedSlots"`
}

// TMMemoryConfig is the configured Flink memory model of a TaskManager.
type TMMemoryConfig struct {
	FrameworkHeap      int64 `json:"frameworkHeap"`
	TaskHeap           int64 `json:"taskHeap"`
	FrameworkOffHeap   int64 `json:"frameworkOffHeap"`
	TaskOffHeap        int64 `json:"taskOffHeap"`
	NetworkMemory      int64 `json:"networkMemory"`
	ManagedMemory      int64 `json:"managedMemory"`
	JVMMetaspace       int64 `json:"jvmMetaspace"`
	JVMOverhead        int64 `json:"jvmOverhead"`
	TotalFlinkMemory   int64 `json:"totalFlinkMemory"`
	TotalProcessMemory int64 `json:"totalProcessMemory"`
}

// AllocatedSlot says which job occupies one of the TM's slots.
type AllocatedSlot struct {
	JobID string `json:"jobId"`
}

// TMMemoryLive is the live side of the memory model, from TM-scope metrics.
type TMMemoryLive struct {
	ManagedUsed, ManagedTotal   int64
	NetworkAvail, NetworkTotal  int64
	MetaspaceUsed, MetaspaceMax int64
}

// AggregatedMetric is one entry of a /subtasks/metrics?get=... response.
type AggregatedMetric struct {
	ID  string  `json:"id"`
	Avg float64 `json:"avg"`
}

// ProfilingInfo is one async-profiler run on a TaskManager (Flink 1.19+).
type ProfilingInfo struct {
	Status     string `json:"status"` // RUNNING / FINISHED / FAILED
	Mode       string `json:"mode"`
	OutputFile string `json:"outputFile"`
	Message    string `json:"message"`
	Duration   int    `json:"duration"`
}

// ConfigEntry is one row of GET /jobmanager/config.
type ConfigEntry struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// Metric is one entry of a /metrics?get=... response.
type Metric struct {
	ID    string `json:"id"`
	Value string `json:"value"`
	// Aggregated endpoints (/taskmanagers/metrics, subtask aggregates)
	// return statistics instead of a single value.
	Min *float64 `json:"min"`
	Max *float64 `json:"max"`
	Avg *float64 `json:"avg"`
	Sum *float64 `json:"sum"`
}

// Float returns the metric's numeric value: the plain value when present,
// otherwise the aggregate average.
func (m Metric) Float() (float64, bool) {
	if m.Value != "" {
		v, err := strconv.ParseFloat(m.Value, 64)
		return v, err == nil
	}
	if m.Avg != nil {
		return *m.Avg, true
	}
	return 0, false
}

// TriggerResponse is the async-operation handle returned by POST
// /jobs/:jid/savepoints and /jobs/:jid/stop.
type TriggerResponse struct {
	RequestID string `json:"request-id"`
}

// SavepointStatus is GET /jobs/:jid/savepoints/:triggerid. It reports both
// plain savepoints and stop-with-savepoint operations.
type SavepointStatus struct {
	Status struct {
		ID string `json:"id"` // IN_PROGRESS or COMPLETED
	} `json:"status"`
	Operation struct {
		Location     string `json:"location"`
		FailureCause struct {
			Class      string `json:"class"`
			StackTrace string `json:"stack-trace"`
		} `json:"failure-cause"`
	} `json:"operation"`
}

// Failed reports whether a completed operation actually failed.
func (s SavepointStatus) Failed() bool {
	return s.Operation.FailureCause.Class != "" || (s.Status.ID == "COMPLETED" && s.Operation.Location == "")
}

// ShortJID abbreviates a 32-char job id for table display.
func ShortJID(jid string) string {
	if len(jid) <= 8 {
		return jid
	}
	return jid[:8] + "…"
}

// FmtDuration renders a millisecond duration as a compact age like 14d02h.
func FmtDuration(ms int64) string {
	if ms < 0 {
		return "-"
	}
	d := time.Duration(ms) * time.Millisecond
	switch {
	case d >= 24*time.Hour:
		return fmt.Sprintf("%dd%02dh", int(d.Hours())/24, int(d.Hours())%24)
	case d >= time.Hour:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	case d >= time.Minute:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	default:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
}

// FmtAge renders a past epoch-millis timestamp as an age relative to now.
func FmtAge(epochMS int64) string {
	if epochMS <= 0 {
		return "-"
	}
	return FmtDuration(time.Since(time.UnixMilli(epochMS)).Milliseconds())
}

// FmtBytes renders a byte count in binary units.
func FmtBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// JMMetrics is a compact snapshot of the JobManager JVM, from
// GET /jobmanager/metrics.
type JMMetrics struct {
	HeapUsed int64
	HeapMax  int64
	CPULoad  float64 // 0..1 process CPU load, -1 when unknown
}

// HeapPct returns heap utilisation as a whole percentage, or -1 when the
// max is unknown.
func (m JMMetrics) HeapPct() int {
	if m.HeapMax <= 0 {
		return -1
	}
	return int(float64(m.HeapUsed) / float64(m.HeapMax) * 100)
}
