package sandbox

import (
	"sync"
	"time"
)

type ExecEvent struct {
	ID         string    `json:"id"`
	AgentID    string    `json:"agent_id"`
	Command    string    `json:"command"`
	WorkDir    string    `json:"work_dir"`
	Stdout     string    `json:"stdout"`
	Stderr     string    `json:"stderr"`
	ExitCode   int       `json:"exit_code"`
	DurationMs int64     `json:"duration_ms"`
	Truncated  bool      `json:"truncated"`
	Timestamp  time.Time `json:"timestamp"`
}

type Broadcaster struct {
	mu          sync.RWMutex
	subscribers map[string]map[chan ExecEvent]struct{}
}

func NewBroadcaster() *Broadcaster {
	return &Broadcaster{
		subscribers: make(map[string]map[chan ExecEvent]struct{}),
	}
}

func (b *Broadcaster) Subscribe(agentID string) (chan ExecEvent, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()

	ch := make(chan ExecEvent, 100)
	if _, ok := b.subscribers[agentID]; !ok {
		b.subscribers[agentID] = make(map[chan ExecEvent]struct{})
	}
	b.subscribers[agentID][ch] = struct{}{}

	unsubscribe := func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if subs, ok := b.subscribers[agentID]; ok {
			delete(subs, ch)
			close(ch)
			if len(subs) == 0 {
				delete(b.subscribers, agentID)
			}
		}
	}

	return ch, unsubscribe
}

func (b *Broadcaster) Publish(event ExecEvent) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if subs, ok := b.subscribers[event.AgentID]; ok {
		for ch := range subs {
			select {
			case ch <- event:
			default:
				// Skip if subscriber channel buffer is full to avoid blocking
			}
		}
	}
}
