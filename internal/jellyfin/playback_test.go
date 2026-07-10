package jellyfin

import (
	"testing"
	"time"
)

func TestPlaybackTrackerCountsElapsedTimeAndIgnoresSeeks(t *testing.T) {
	now := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	tracker := newPlaybackTracker()
	tracker.now = func() time.Time { return now }

	if got := tracker.observe("session", 600, false).playDuration; got != 0 {
		t.Fatalf("initial duration = %v", got)
	}
	now = now.Add(10 * time.Second)
	if got := tracker.observe("session", 610, false).playDuration; got != 10 {
		t.Fatalf("normal progress duration = %v", got)
	}

	now = now.Add(time.Second)
	if got := tracker.observe("session", 1200, false).playDuration; got != 0 {
		t.Fatalf("seek duration = %v", got)
	}
	now = now.Add(5 * time.Second)
	if got := tracker.observe("session", 1205, false).playDuration; got != 5 {
		t.Fatalf("post-seek duration = %v", got)
	}
}

func TestPlaybackTrackerSuppressesDuplicateStops(t *testing.T) {
	now := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	tracker := newPlaybackTracker()
	tracker.now = func() time.Time { return now }

	tracker.observe("session", 10, false)
	now = now.Add(5 * time.Second)
	first := tracker.observe("session", 15, true)
	if first.duplicate || first.playDuration != 5 {
		t.Fatalf("first stop = %#v", first)
	}

	now = now.Add(time.Second)
	if duplicate := tracker.observe("session", 15, true); !duplicate.duplicate {
		t.Fatalf("duplicate stop = %#v", duplicate)
	}
}
