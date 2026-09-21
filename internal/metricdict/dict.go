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

	// cluster / JobManager
	{"numRunningJobs", Info{"cluster", "count", "jobs currently running"}},
	{"taskSlotsAvailable", Info{"cluster", "count", "free task slots — 0 means new jobs/rescales wait"}},
	{"taskSlotsTotal", Info{"cluster", "count", "task slots across all TaskManagers"}},

	// JVM memory & runtime
	{"Status.JVM.CPU.Time", Info{"cpu", "ns", "cumulative JVM CPU time"}},
	{"Status.JVM.Memory.NonHeap.Used", Info{"memory", "bytes", "non-heap in use (metaspace, code cache, …)"}},
	{"Status.JVM.Memory.NonHeap.Max", Info{"memory", "bytes", "non-heap ceiling"}},
	{"Status.JVM.Memory.Metaspace.Used", Info{"memory", "bytes", "metaspace in use — steady growth on a session cluster = classloader leak"}},
	{"Status.JVM.Memory.Metaspace.Max", Info{"memory", "bytes", "metaspace ceiling (taskmanager.memory.jvm-metaspace.size)"}},
	{"Status.JVM.Memory.Direct.MemoryUsed", Info{"memory", "bytes", "direct (off-heap) buffers in use"}},
	{"Status.JVM.Memory.Direct.TotalCapacity", Info{"memory", "bytes", "direct buffer capacity"}},
	{"Status.JVM.Memory.Direct.Count", Info{"memory", "count", "direct buffers allocated"}},
	{"Status.JVM.Memory.Mapped.MemoryUsed", Info{"memory", "bytes", "memory-mapped buffers in use"}},
	{"Status.JVM.ClassLoader.ClassesLoaded", Info{"runtime", "count", "classes loaded since start"}},
	{"Status.JVM.ClassLoader.ClassesUnloaded", Info{"runtime", "count", "classes unloaded — near 0 while loaded climbs = leak"}},
	{"Status.Flink.Memory.Managed.Used", Info{"memory", "bytes", "managed memory in use (RocksDB, batch operators)"}},
	{"Status.Flink.Memory.Managed.Total", Info{"memory", "bytes", "managed memory budget"}},

	// network stack
	{"Status.Shuffle.Netty.AvailableMemorySegments", Info{"network", "count", "free network buffers — 0 = shuffle is starved"}},
	{"Status.Shuffle.Netty.UsedMemorySegments", Info{"network", "count", "network buffers in use"}},
	{"Status.Shuffle.Netty.TotalMemorySegments", Info{"network", "count", "network buffers configured"}},
	{"Status.Shuffle.Netty.AvailableMemory", Info{"network", "bytes", "free network memory"}},
	{"Status.Shuffle.Netty.UsedMemory", Info{"network", "bytes", "network memory in use"}},
	{"Status.Shuffle.Netty.TotalMemory", Info{"network", "bytes", "network memory configured"}},
	{"inputExclusiveBuffersUsage", Info{"network", "ratio", "exclusive input buffers in use"}},
	{"inputFloatingBuffersUsage", Info{"network", "ratio", "floating input buffers in use — high = upstream outpaces this task"}},
	{"outputQueueLength", Info{"network", "count", "queued output buffers"}},
	{"inputQueueSize", Info{"network", "bytes", "queued input data"}},
	{"outputQueueSize", Info{"network", "bytes", "queued output data"}},
	{"debloatedBufferSize", Info{"network", "bytes", "buffer size chosen by buffer debloating"}},
	{"estimatedTimeToConsumeBuffersMs", Info{"network", "ms", "time to drain in-flight data — what aligned checkpoints wait for"}},
	{"numBuffersOutPerSecond", Info{"network", "buf/s", "output buffers sent"}},

	// task IO & time accounting
	{"numRecordsIn", Info{"throughput", "count", "records received (cumulative)"}},
	{"numRecordsOut", Info{"throughput", "count", "records emitted (cumulative)"}},
	{"numBytesIn", Info{"throughput", "bytes", "bytes received (cumulative)"}},
	{"numBytesOut", Info{"throughput", "bytes", "bytes emitted (cumulative)"}},
	{"numLateRecordsDropped", Info{"watermarks", "count", "records dropped as late by a window"}},
	{"numRecordsInErrors", Info{"connector", "count", "source records that failed to deserialize"}},
	{"softBackPressuredTimeMsPerSecond", Info{"backpressure", "ms/s", "backpressured but still making progress"}},
	{"hardBackPressuredTimeMsPerSecond", Info{"backpressure", "ms/s", "fully blocked by downstream"}},
	{"maxSoftBackPressureTimeMs", Info{"backpressure", "ms", "longest soft-backpressure stretch"}},
	{"maxHardBackPressureTimeMs", Info{"backpressure", "ms", "longest hard-backpressure stretch"}},
	{"accumulatedBusyTime", Info{"backpressure", "ms", "busy time since start"}},
	{"accumulatedBackPressuredTime", Info{"backpressure", "ms", "backpressured time since start"}},
	{"accumulatedIdleTime", Info{"backpressure", "ms", "idle time since start"}},
	{"changelogBusyTimeMsPerSecond", Info{"state", "ms/s", "time spent uploading the state changelog"}},

	// job status (Flink 1.16+ state-time metrics)
	{"restartingTime", Info{"availability", "ms", "time in the current RESTARTING state"}},
	{"runningTime", Info{"availability", "ms", "time in the current RUNNING state"}},
	{"failingTime", Info{"availability", "ms", "time in the current FAILING state"}},
	{"cancellingTime", Info{"availability", "ms", "time in the current CANCELLING state"}},
	{"initializingTime", Info{"availability", "ms", "time spent initializing (state restore)"}},
	{"deployingTime", Info{"availability", "ms", "time spent deploying tasks"}},
	{"fullRestarts", Info{"availability", "count", "full job restarts (deprecated; see numRestarts)"}},

	// checkpoints
	{"lastCheckpointFullSize", Info{"ckpt", "bytes", "full state size of the last checkpoint (vs incremental size)"}},
	{"lastCheckpointProcessedData", Info{"ckpt", "bytes", "data processed during alignment"}},
	{"lastCheckpointPersistedData", Info{"ckpt", "bytes", "in-flight data persisted (unaligned)"}},
	{"lastCheckpointExternalPath", Info{"ckpt", "path", "where the last checkpoint was written"}},
	{"lastCheckpointRestoreTimestamp", Info{"ckpt", "epoch ms", "when the job last restored from a checkpoint"}},
	{"lastCompletedCheckpointId", Info{"ckpt", "id", "id of the newest completed checkpoint"}},
	{"numberOfInProgressCheckpoints", Info{"ckpt", "count", "checkpoints in flight — stuck at 1 = barrier stalled"}},
	{"totalNumberOfCheckpoints", Info{"ckpt", "count", "checkpoints triggered"}},

	// sources (FLIP-33) & Kafka
	{"currentFetchEventTimeLag", Info{"connector", "ms", "event-time lag at fetch"}},
	{"watermarkAlignmentDrift", Info{"watermarks", "ms", "how far this source's watermark is ahead of the aligned group"}},
	{"KafkaSourceReader.commitsFailed", Info{"kafka", "count", "offset commits failed (checkpoint-bound; offsets in Kafka lag)"}},
	{"currentOffset", Info{"kafka", "offset", "consumer position of the partition"}},
	{"committedOffset", Info{"kafka", "offset", "offset committed back to Kafka on checkpoint"}},
	{"records-consumed-rate", Info{"kafka", "rec/s", "Kafka consumer fetch rate"}},
	{"bytes-consumed-rate", Info{"kafka", "B/s", "Kafka consumer byte rate"}},
	{"fetch-latency-avg", Info{"kafka", "ms", "average fetch request latency"}},

	// RocksDB native metrics (state.backend.rocksdb.metrics.*)
	{"rocksdb.estimate-num-keys", Info{"state", "count", "estimated keys in this column family"}},
	{"rocksdb.estimate-live-data-size", Info{"state", "bytes", "estimated live data"}},
	{"rocksdb.total-sst-files-size", Info{"state", "bytes", "SST files on disk"}},
	{"rocksdb.num-running-compactions", Info{"state", "count", "compactions running"}},
	{"rocksdb.compaction-pending", Info{"state", "bool", "compaction queued — sustained 1 = compaction can't keep up"}},
	{"rocksdb.mem-table-flush-pending", Info{"state", "bool", "memtable flush queued"}},
	{"rocksdb.block-cache-usage", Info{"state", "bytes", "block cache in use"}},
	{"rocksdb.background-errors", Info{"state", "count", "background errors — any is serious"}},
	{"rocksdb.actual-delayed-write-rate", Info{"state", "B/s", "write stall throttle; >0 = RocksDB is slowing writes"}},
	{"rocksdb.is-write-stopped", Info{"state", "bool", "writes stopped by RocksDB (stall)"}},
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
