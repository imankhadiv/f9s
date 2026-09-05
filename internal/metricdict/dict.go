// Package metricdict is f9s's curated metric dictionary: suffix-matched
// (operator names are embedded in metric IDs and unstable, so full-ID
// matching would break) descriptions, units, and categories for the metrics
// operators actually reach for.
package metricdict

import "strings"

// Info describes one metric pattern.
type Info struct {
	Category string
	Unit     string
	Desc     string
}

var entries = []struct {
	suffix string
	info   Info
}{
	{"Status.JVM.CPU.Load", Info{"cpu", "ratio", "recent JVM CPU usage (0-1)"}},
	{"Status.JVM.Memory.Heap.Used", Info{"memory", "bytes", "heap in use"}},
	{"Status.JVM.Memory.Heap.Max", Info{"memory", "bytes", "heap ceiling"}},
	{"Status.JVM.Threads.Count", Info{"threads", "count", "live JVM threads"}},
	{".Time", Info{"gc", "ms", "cumulative GC time (per collector)"}},
	{".Count", Info{"gc", "count", "GC runs (per collector)"}},
	{"numRegisteredTaskManagers", Info{"cluster", "count", "registered TaskManagers"}},
	{"numRestarts", Info{"availability", "count", "job restarts since submission"}},
	{"uptime", Info{"availability", "ms", "time since last restart"}},
	{"downtime", Info{"availability", "ms", "time in recovery"}},
	{"lastCheckpointDuration", Info{"ckpt", "ms", "duration of the last checkpoint"}},
	{"lastCheckpointSize", Info{"ckpt", "bytes", "size of the last checkpoint"}},
	{"numberOfFailedCheckpoints", Info{"ckpt", "count", "failed checkpoints"}},
	{"numberOfCompletedCheckpoints", Info{"ckpt", "count", "completed checkpoints"}},
	{"checkpointStartDelayNanos", Info{"ckpt", "ns", "start delay; one slow subtask = data skew"}},
	{"numRecordsInPerSecond", Info{"throughput", "rec/s", "input record rate"}},
	{"numRecordsOutPerSecond", Info{"throughput", "rec/s", "output record rate"}},
	{"numBytesInPerSecond", Info{"throughput", "B/s", "input byte rate"}},
	{"numBytesOutPerSecond", Info{"throughput", "B/s", "output byte rate"}},
	{"busyTimeMsPerSecond", Info{"backpressure", "ms/s", "busy time; 1000 = saturated"}},
	{"backPressuredTimeMsPerSecond", Info{"backpressure", "ms/s", "time spent backpressured"}},
	{"idleTimeMsPerSecond", Info{"backpressure", "ms/s", "idle time"}},
	{"isBackPressured", Info{"backpressure", "bool", "currently backpressured"}},
	{"mailboxLatencyMs", Info{"runtime", "ms", "mailbox wait — early backpressure warning"}},
	{"mailboxQueueSize", Info{"runtime", "count", "queued mailbox actions"}},
	{"currentInputWatermark", Info{"watermarks", "epoch ms", "last watermark received"}},
	{"currentOutputWatermark", Info{"watermarks", "epoch ms", "last watermark emitted"}},
	{"watermarkLag", Info{"watermarks", "ms", "watermark behind wall clock"}},
	{"currentEmitEventTimeLag", Info{"connector", "ms", "event-time lag at emit"}},
	{"sourceIdleTime", Info{"connector", "ms", "source idle span"}},
	{"pendingRecords", Info{"connector", "count", "backlog not yet fetched (e.g. Kafka)"}},
	{"records-lag-max", Info{"kafka", "count", "max consumer partition lag"}},
	{"commitsSucceeded", Info{"kafka", "count", "offset commits succeeded"}},
	{"inPoolUsage", Info{"network", "ratio", "input buffer pool usage (1.0 = full)"}},
	{"outPoolUsage", Info{"network", "ratio", "output buffer pool usage"}},
	{"inputQueueLength", Info{"network", "count", "queued input buffers"}},
}

// Lookup finds the longest suffix match for a metric ID; ok is false for
// unknown metrics.
func Lookup(id string) (Info, bool) {
	best := -1
	var out Info
	for _, e := range entries {
		if strings.HasSuffix(id, e.suffix) && len(e.suffix) > best {
			best = len(e.suffix)
			out = e.info
		}
	}
	return out, best >= 0
}
