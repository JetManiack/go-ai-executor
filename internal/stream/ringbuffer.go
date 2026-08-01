package stream

// DefaultStreamBufferBytes is how much recent output is retained per sandbox for
// replay to a connecting watcher.
//
// Some buffer is required for the emergency-stop button to be usable at all:
// with none, an administrator responding to an alert opens the terminal and sees
// an empty screen, unable to tell a command that is still thinking from one that
// already finished, and has nothing to base "kill it or not" on.
const DefaultStreamBufferBytes = 256 << 10 // 256 KiB

// ring is a per-sandbox bounded FIFO of recent events, evicting oldest-first
// once its byte budget is exceeded. It is not safe for concurrent use; the
// broadcaster owns one per sandbox and serializes access.
type ring struct {
	limitBytes int
	bytes      int
	events     []Event

	// nextSeq is the sequence number the next appended event receives. It keeps
	// counting past evictions, so a watcher's position stays meaningful even
	// after the events around it are gone.
	nextSeq uint64
}

func newRing(limitBytes int) *ring {
	if limitBytes <= 0 {
		limitBytes = DefaultStreamBufferBytes
	}
	return &ring{limitBytes: limitBytes, nextSeq: 1}
}

// append stamps e with the next sequence number, stores it, and returns the
// stamped event.
//
// An event larger than the whole budget is stored anyway, evicting everything
// else: dropping it instead would lose the single most recent thing that
// happened, which is what a watcher most needs.
func (r *ring) append(e Event) Event {
	e.Seq = r.nextSeq
	r.nextSeq++

	r.events = append(r.events, e)
	r.bytes += e.size()

	for len(r.events) > 1 && r.bytes > r.limitBytes {
		r.bytes -= r.events[0].size()
		r.events = r.events[1:]
	}
	return e
}

// since returns the retained events after seq, together with how many events
// between seq and the oldest retained one were evicted before the caller asked.
//
// A non-zero missed count is what the stream reports as an EventGap. Resuming
// silently would draw a continuous terminal that never happened — and a fresh
// watcher (seq 0) is told too, because otherwise the top of their screen looks
// like the start of the session when it is really just the start of what was
// retained.
func (r *ring) since(seq uint64) (events []Event, missed uint64) {
	if len(r.events) == 0 {
		return nil, 0
	}

	oldest := r.events[0].Seq
	if oldest > seq+1 {
		missed = oldest - seq - 1
	}

	for i, e := range r.events {
		if e.Seq > seq {
			return r.events[i:], missed
		}
	}
	// Caller is already up to date (or ahead, after a server restart reset the
	// counter); nothing to replay.
	return nil, missed
}

// lastSeq returns the sequence number of the most recent retained event, or 0
// when nothing is retained.
func (r *ring) lastSeq() uint64 {
	if len(r.events) == 0 {
		return 0
	}
	return r.events[len(r.events)-1].Seq
}
