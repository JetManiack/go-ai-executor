package sandbox

import (
	"errors"
	"fmt"
	"path/filepath"
	"sync"
)

// Manager owns one sandbox per agent and the event bus their output is streamed
// through.
type Manager struct {
	baseConfig  Config
	broadcaster *Broadcaster

	mu        sync.RWMutex
	instances map[string]*Sandbox
}

func NewManager(cfg Config) (*Manager, error) {
	if cfg.RootDir == "" {
		return nil, errors.New("root directory cannot be empty")
	}

	absRoot, err := filepath.Abs(cfg.RootDir)
	if err != nil {
		return nil, fmt.Errorf("resolve sandbox root: %w", err)
	}
	cfg.RootDir = absRoot

	return &Manager{
		baseConfig:  cfg,
		broadcaster: NewBroadcaster(cfg.StreamBufferBytes),
		instances:   make(map[string]*Sandbox),
	}, nil
}

// Broadcaster returns the manager's event bus, which fans sandbox output out to
// the humans watching a terminal.
func (m *Manager) Broadcaster() *Broadcaster { return m.broadcaster }

// GetSandbox returns agentID's sandbox, creating it on first use.
//
// An empty agentID is rejected rather than mapped to the shared root: the root
// contains every agent's directory, so handing it out on a missing identity
// would turn one unauthenticated call into access to all of them.
func (m *Manager) GetSandbox(agentID string) (*Sandbox, error) {
	if agentID == "" {
		return nil, errors.New("sandbox requires an agent id")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if sb, ok := m.instances[agentID]; ok {
		return sb, nil
	}

	cfg := m.baseConfig
	cfg.RootDir = filepath.Join(m.baseConfig.RootDir, "agents", agentID)

	sb, err := newSandbox(agentID, cfg, m.broadcaster)
	if err != nil {
		return nil, fmt.Errorf("create sandbox for agent %s: %w", agentID, err)
	}

	m.instances[agentID] = sb
	return sb, nil
}

// LiveSandboxes returns every sandbox instantiated so far, keyed by agent ID.
// Sandboxes are created lazily on an agent's first tool call, so an agent that
// has never connected since this process started is legitimately absent.
func (m *Manager) LiveSandboxes() map[string]*Sandbox {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make(map[string]*Sandbox, len(m.instances))
	for id, sb := range m.instances {
		out[id] = sb
	}
	return out
}

// KillSandbox tears down every process running in agentID's sandbox and reports
// how many process groups it signalled.
//
// An agent with no sandbox yet is not an error: it has nothing running, which is
// the state the caller asked for. Blocking such an agent is still meaningful —
// the block is what stops its next call.
func (m *Manager) KillSandbox(agentID, byActor, reason string) (int, error) {
	m.mu.RLock()
	sb, ok := m.instances[agentID]
	m.mu.RUnlock()
	if !ok {
		return 0, nil
	}
	return sb.KillAll(byActor, reason)
}
