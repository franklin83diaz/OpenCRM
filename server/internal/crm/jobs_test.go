package crm

import (
	"testing"
	"time"
)

func TestJobsBookableAt(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 30, 25, 0, time.UTC)
	nextMinute := time.Date(2026, 10, 14, 12, 31, 0, 0, time.UTC)
	cases := []struct {
		name  string
		runAt time.Time
		want  time.Time
	}{
		{"in the current minute", time.Date(2026, 10, 14, 12, 30, 40, 0, time.UTC), nextMinute},
		{"in the past", time.Date(2026, 10, 14, 11, 0, 0, 0, time.UTC), nextMinute},
		{"exactly now", now, nextMinute},
		{"exactly the next minute", nextMinute, nextMinute},
		{"later", time.Date(2026, 10, 14, 13, 0, 0, 0, time.UTC), time.Date(2026, 10, 14, 13, 0, 0, 0, time.UTC)},
	}
	for _, c := range cases {
		if got := BookableAt(c.runAt, now); !got.Equal(c.want) {
			t.Errorf("%s: BookableAt = %s, want %s", c.name, got, c.want)
		}
	}
	// On a minute boundary the current minute is this instant, so the next is a full minute on.
	boundary := time.Date(2026, 10, 14, 12, 30, 0, 0, time.UTC)
	if got := BookableAt(boundary, boundary); !got.Equal(nextMinute) {
		t.Errorf("on the boundary: %s", got)
	}
	// Before the epoch, where Go's integer division truncates the wrong way.
	if got := floorDiv(-1, 60_000); got != -1 {
		t.Errorf("floorDiv(-1) = %d", got)
	}
}
