package stream

import (
	"fmt"
	"strings"
	"testing"
)

func chunk(sandboxID, data string) Event {
	return Event{SandboxID: sandboxID, Kind: EventStdout, Data: data}
}

func TestPublishAssignsMonotonicSequencePerSandbox(t *testing.T) {
	b := NewBroadcaster(0)

	for i := uint64(1); i <= 3; i++ {
		got := b.Publish(chunk("a", "x"))
		if got.Seq != i {
			t.Errorf("event %d: seq = %d, want %d", i, got.Seq, i)
		}
	}

	// Sequence numbers are per sandbox, so one busy agent cannot make another's
	// resume position jump.
	if first := b.Publish(chunk("b", "x")); first.Seq != 1 {
		t.Errorf("first event of second sandbox: seq = %d, want 1", first.Seq)
	}
}

func TestSubscribeReplaysRetainedEvents(t *testing.T) {
	b := NewBroadcaster(0)
	b.Publish(chunk("a", "one"))
	b.Publish(chunk("a", "two"))

	replay, sub := b.Subscribe("a", 0)
	defer b.Unsubscribe("a", sub)

	if len(replay) != 2 {
		t.Fatalf("replay = %d events, want 2", len(replay))
	}
	if replay[0].Data != "one" || replay[1].Data != "two" {
		t.Errorf("replay out of order: %q, %q", replay[0].Data, replay[1].Data)
	}
}

func TestSubscribeResumesAfterSequence(t *testing.T) {
	b := NewBroadcaster(0)
	b.Publish(chunk("a", "one"))
	second := b.Publish(chunk("a", "two"))
	b.Publish(chunk("a", "three"))

	replay, sub := b.Subscribe("a", second.Seq)
	defer b.Unsubscribe("a", sub)

	if len(replay) != 1 || replay[0].Data != "three" {
		t.Fatalf("replay = %+v, want just the event after seq %d", replay, second.Seq)
	}
}

func TestSubscribeDeliversLiveEventsAfterReplay(t *testing.T) {
	b := NewBroadcaster(0)
	b.Publish(chunk("a", "old"))

	replay, sub := b.Subscribe("a", 0)
	defer b.Unsubscribe("a", sub)
	if len(replay) != 1 {
		t.Fatalf("replay = %d events, want 1", len(replay))
	}

	b.Publish(chunk("a", "new"))
	select {
	case got := <-sub.Events():
		if got.Data != "new" {
			t.Errorf("live event data = %q, want %q", got.Data, "new")
		}
	default:
		t.Fatal("live event was not delivered to the subscriber")
	}
}

// TestEvictionIsReportedAsAGap is the honesty requirement: a watcher whose
// position has been evicted must be told output is missing, rather than shown a
// continuous terminal that never happened.
func TestEvictionIsReportedAsAGap(t *testing.T) {
	// Small budget so a handful of events overflow it.
	b := NewBroadcaster(1024)

	first := b.Publish(chunk("a", strings.Repeat("x", 512)))
	for range 5 {
		b.Publish(chunk("a", strings.Repeat("y", 512)))
	}

	replay, sub := b.Subscribe("a", first.Seq)
	defer b.Unsubscribe("a", sub)

	if len(replay) == 0 {
		t.Fatal("replay is empty")
	}
	if replay[0].Kind != EventGap {
		t.Fatalf("first replayed event kind = %q, want %q", replay[0].Kind, EventGap)
	}
	if replay[0].MissedEvents == 0 {
		t.Error("gap marker reports zero missed events")
	}
	for _, e := range replay[1:] {
		if e.Kind == EventGap {
			t.Error("more than one gap marker in a single replay")
		}
	}
}

func TestNoGapWhenNothingWasEvicted(t *testing.T) {
	b := NewBroadcaster(0)
	b.Publish(chunk("a", "one"))
	b.Publish(chunk("a", "two"))

	replay, sub := b.Subscribe("a", 0)
	defer b.Unsubscribe("a", sub)

	for _, e := range replay {
		if e.Kind == EventGap {
			t.Fatalf("gap reported although the whole stream is retained: %+v", e)
		}
	}
}

// TestSlowConsumerIsDisconnected covers the behavior that replaced silently
// dropping events: losing one chunk of a terminal and carrying on would corrupt
// the output a human is deciding from, so the watcher is disconnected instead and
// told why.
func TestSlowConsumerIsDisconnected(t *testing.T) {
	b := NewBroadcaster(64 << 20) // large enough that retention is not what ends this
	_, sub := b.Subscribe("a", 0)
	defer b.Unsubscribe("a", sub)

	// Never read from sub, so its queue fills and the next publish evicts it.
	for i := range subscriberQueue + 10 {
		b.Publish(chunk("a", fmt.Sprintf("event-%d", i)))
	}

	drained := 0
	for range sub.Events() {
		drained++
	}

	if !sub.Lagged() {
		t.Error("subscription ended without being marked as lagged")
	}
	if drained > subscriberQueue {
		t.Errorf("drained %d events, want at most the queue depth %d", drained, subscriberQueue)
	}
	if b.WatcherCount("a") != 0 {
		t.Errorf("watcher count = %d, want 0 after the slow consumer was dropped", b.WatcherCount("a"))
	}
}

func TestUnsubscribeClosesWithoutMarkingLagged(t *testing.T) {
	b := NewBroadcaster(0)
	_, sub := b.Subscribe("a", 0)

	b.Unsubscribe("a", sub)

	if _, open := <-sub.Events(); open {
		t.Error("events channel still open after Unsubscribe")
	}
	if sub.Lagged() {
		t.Error("deliberate unsubscribe was reported as lag, which would make the client reconnect needlessly")
	}
	// Unsubscribing twice must not panic on a double close.
	b.Unsubscribe("a", sub)
}

func TestPublishOnlyReachesItsOwnSandbox(t *testing.T) {
	b := NewBroadcaster(0)
	_, subA := b.Subscribe("a", 0)
	defer b.Unsubscribe("a", subA)
	_, subB := b.Subscribe("b", 0)
	defer b.Unsubscribe("b", subB)

	b.Publish(chunk("a", "for a"))

	select {
	case got := <-subB.Events():
		t.Fatalf("sandbox b's watcher received sandbox a's event: %+v", got)
	default:
	}
	select {
	case got := <-subA.Events():
		if got.Data != "for a" {
			t.Errorf("data = %q, want %q", got.Data, "for a")
		}
	default:
		t.Fatal("sandbox a's watcher received nothing")
	}
}

func TestWatcherCountTracksSubscriptions(t *testing.T) {
	b := NewBroadcaster(0)
	if got := b.WatcherCount("a"); got != 0 {
		t.Fatalf("initial watcher count = %d, want 0", got)
	}

	_, first := b.Subscribe("a", 0)
	_, second := b.Subscribe("a", 0)
	if got := b.WatcherCount("a"); got != 2 {
		t.Errorf("watcher count = %d, want 2", got)
	}

	b.Unsubscribe("a", first)
	if got := b.WatcherCount("a"); got != 1 {
		t.Errorf("watcher count after one leaves = %d, want 1", got)
	}
	b.Unsubscribe("a", second)
	if got := b.WatcherCount("a"); got != 0 {
		t.Errorf("watcher count after all leave = %d, want 0", got)
	}
}

func TestRingKeepsTheNewestEventEvenWhenOversized(t *testing.T) {
	r := newRing(128)
	// An event bigger than the whole budget must still be retained: dropping it
	// would lose the most recent thing that happened, which is what a watcher
	// most needs.
	big := r.append(chunk("a", strings.Repeat("x", 4096)))

	events, _ := r.since(0)
	if len(events) != 1 || events[0].Seq != big.Seq {
		t.Errorf("retained = %+v, want just the oversized newest event", events)
	}
}

func TestRingSinceWhenCallerIsUpToDate(t *testing.T) {
	r := newRing(0)
	last := r.append(chunk("a", "one"))

	events, missed := r.since(last.Seq)
	if len(events) != 0 {
		t.Errorf("events = %+v, want none for an up-to-date caller", events)
	}
	if missed != 0 {
		t.Errorf("missed = %d, want 0", missed)
	}
}
