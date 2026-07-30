package sandbox

import (
	"sync"
	"time"
)

// subscriberQueue is how many events a watcher may fall behind by before it is
// disconnected as a slow consumer. Sized for a browser briefly descheduled or on
// a slow link, not for one that has stopped reading: a larger queue only delays
// the disconnect while growing the memory held per watcher.
const subscriberQueue = 256

// Broadcaster fans sandbox events out to the humans watching a terminal, and
// retains recent events per sandbox so a watcher connecting mid-command sees
// context instead of a blank screen.
type Broadcaster struct {
	mu          sync.Mutex
	limitBytes  int
	rings       map[string]*ring
	subscribers map[string]map[*Subscription]struct{}
}

// NewBroadcaster returns a Broadcaster retaining up to limitBytes of recent
// output per sandbox; zero selects DefaultStreamBufferBytes.
func NewBroadcaster(limitBytes int) *Broadcaster {
	if limitBytes <= 0 {
		limitBytes = DefaultStreamBufferBytes
	}
	return &Broadcaster{
		limitBytes:  limitBytes,
		rings:       make(map[string]*ring),
		subscribers: make(map[string]map[*Subscription]struct{}),
	}
}

// Subscription is one watcher's view of one sandbox.
//
// The events channel is closed when the subscription ends, for any reason.
// Lagged distinguishes the reasons: true means the watcher could not keep up and
// was disconnected, and should reconnect from its last seen sequence number —
// which will report the resulting gap.
type Subscription struct {
	events chan Event

	mu     sync.Mutex
	closed bool
	lagged bool
}

// Events returns the channel live events arrive on. It is closed when the
// subscription ends.
func (s *Subscription) Events() <-chan Event { return s.events }

// Lagged reports whether the subscription ended because the watcher fell behind,
// as opposed to being closed deliberately.
func (s *Subscription) Lagged() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lagged
}

// close ends the subscription exactly once. Both the watcher (via
// Broadcaster.Unsubscribe) and the publisher (on lag) can reach it.
func (s *Subscription) close(lagged bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	s.lagged = lagged
	close(s.events)
}

// Subscribe registers a watcher on sandboxID resuming after sequence number
// after, and returns the retained events it missed alongside a live
// subscription.
//
// Replay and registration happen under one lock, so an event published between
// the two cannot fall through the crack between "history" and "live".
func (b *Broadcaster) Subscribe(sandboxID string, after uint64) ([]Event, *Subscription) {
	b.mu.Lock()
	defer b.mu.Unlock()

	retained, missed := b.ringFor(sandboxID).since(after)

	replay := make([]Event, 0, len(retained)+1)
	if missed > 0 {
		replay = append(replay, Event{
			SandboxID:    sandboxID,
			Kind:         EventGap,
			At:           time.Now().UTC(),
			MissedEvents: missed,
		})
	}
	replay = append(replay, retained...)

	sub := &Subscription{events: make(chan Event, subscriberQueue)}
	if b.subscribers[sandboxID] == nil {
		b.subscribers[sandboxID] = make(map[*Subscription]struct{})
	}
	b.subscribers[sandboxID][sub] = struct{}{}

	return replay, sub
}

// Unsubscribe ends sub and stops delivering to it.
func (b *Broadcaster) Unsubscribe(sandboxID string, sub *Subscription) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.removeLocked(sandboxID, sub, false)
}

// Publish stamps e with the sandbox's next sequence number, retains it, and
// delivers it to every current watcher. It returns the stamped event.
//
// A watcher whose queue is full is disconnected rather than skipped. Dropping one
// event of a chunked output stream and carrying on would silently corrupt the
// terminal a human is making a kill-or-not decision from; disconnecting makes the
// loss explicit, and the reconnect reports it as a gap.
func (b *Broadcaster) Publish(e Event) Event {
	b.mu.Lock()
	defer b.mu.Unlock()

	stamped := b.ringFor(e.SandboxID).append(e)

	for sub := range b.subscribers[e.SandboxID] {
		select {
		case sub.events <- stamped:
		default:
			b.removeLocked(e.SandboxID, sub, true)
		}
	}
	return stamped
}

// LastSeq returns the most recent sequence number retained for sandboxID, or 0
// if nothing is.
func (b *Broadcaster) LastSeq(sandboxID string) uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.ringFor(sandboxID).lastSeq()
}

// WatcherCount returns how many watchers are currently attached to sandboxID.
func (b *Broadcaster) WatcherCount(sandboxID string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subscribers[sandboxID])
}

// ringFor returns sandboxID's ring, creating it on first use. Callers must hold
// b.mu.
func (b *Broadcaster) ringFor(sandboxID string) *ring {
	r, ok := b.rings[sandboxID]
	if !ok {
		r = newRing(b.limitBytes)
		b.rings[sandboxID] = r
	}
	return r
}

// removeLocked detaches sub and closes it. Callers must hold b.mu.
func (b *Broadcaster) removeLocked(sandboxID string, sub *Subscription, lagged bool) {
	subs, ok := b.subscribers[sandboxID]
	if !ok {
		return
	}
	if _, ok := subs[sub]; !ok {
		return
	}
	delete(subs, sub)
	if len(subs) == 0 {
		delete(b.subscribers, sandboxID)
	}
	sub.close(lagged)
}
