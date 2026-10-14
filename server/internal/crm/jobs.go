package crm

import "time"

// Runs booked on the platform queue. The queue delivers due jobs once a minute,
// and the booking key here is the target minute, which the queue keeps for good
// whatever became of its job: a repeat gets the job it already has.

// BookableAt is the earliest a run can be booked for: the next minute, never
// the current one. A run books its follow-up while its own job is still being
// delivered, and that job's minute is this minute or earlier; booking the same
// minute would hand the run its own job back and the chain would stop. The
// queue would only deliver at the next minute anyway, so nothing is slower.
func BookableAt(runAt, now time.Time) time.Time {
	next := floorDiv(now.UnixMilli(), 60_000)*60_000 + 60_000
	if runAt.UnixMilli() < next {
		return time.UnixMilli(next).UTC()
	}
	return runAt
}

// floorDiv is Math.floor(a / b) for whole numbers: Go's / truncates toward zero.
func floorDiv(a, b int64) int64 {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}
