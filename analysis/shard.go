package analysis

import "hash/fnv"

// shardForPID maps a backend PID to one of n shards. Sharding the entry
// stream by PID (rather than round-robin) is what lets each analyzer keep
// its per-backend correlation intact: every entry of a given backend —
// the primary LOG/ERROR plus its DETAIL/HINT/STATEMENT continuations, plus
// the "still waiting" → "acquired" lock pairs, plus connection sessions —
// lands on the same shard, in stream order. Cross-PID aggregation is then
// recombined by each analyzer's Merge.
//
// Entries without an extractable PID all route to shard 0. They carry no
// per-backend state to correlate (a continuation with no PID is dropped by
// every analyzer), so concentrating them is harmless and keeps the mapping
// total and deterministic.
func shardForPID(pid string, n int) int {
	if n <= 1 || pid == "" {
		return 0
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(pid))
	return int(h.Sum32() % uint32(n))
}
