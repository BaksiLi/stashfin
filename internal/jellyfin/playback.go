package jellyfin

import (
	"sync"
	"time"
)

const (
	playbackSessionTTL = 12 * time.Hour
	duplicateStopTTL   = time.Minute
	seekTolerance      = 5 * time.Second
)

type playbackState struct {
	position  float64
	updatedAt time.Time
}

type playbackUpdate struct {
	playDuration float64
	duplicate    bool
}

type playbackTracker struct {
	mu        sync.Mutex
	sessions  map[string]playbackState
	completed map[string]time.Time
	now       func() time.Time
}

func newPlaybackTracker() *playbackTracker {
	return &playbackTracker{
		sessions:  make(map[string]playbackState),
		completed: make(map[string]time.Time),
		now:       time.Now,
	}
}

func (t *playbackTracker) observe(key string, position float64, stopped bool) playbackUpdate {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := t.now()
	t.prune(now)
	if position < 0 {
		position = 0
	}
	if stopped {
		if completedAt, ok := t.completed[key]; ok && now.Sub(completedAt) < duplicateStopTTL {
			return playbackUpdate{duplicate: true}
		}
		t.completed[key] = now
	}

	previous, ok := t.sessions[key]
	update := playbackUpdate{}
	if ok && position >= previous.position {
		positionDelta := position - previous.position
		wallDelta := now.Sub(previous.updatedAt).Seconds()
		if positionDelta <= wallDelta+seekTolerance.Seconds() {
			update.playDuration = positionDelta
		}
	}

	if stopped {
		delete(t.sessions, key)
	} else {
		t.sessions[key] = playbackState{position: position, updatedAt: now}
	}
	return update
}

func (t *playbackTracker) prune(now time.Time) {
	for key, state := range t.sessions {
		if now.Sub(state.updatedAt) > playbackSessionTTL {
			delete(t.sessions, key)
		}
	}
	for key, completedAt := range t.completed {
		if now.Sub(completedAt) > playbackSessionTTL {
			delete(t.completed, key)
		}
	}
}
