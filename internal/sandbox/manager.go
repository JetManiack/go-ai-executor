package sandbox

import (
	"errors"
	"fmt"
	"path/filepath"
	"sync"
)

// Manager owns one sandbox per agent and the sink their output is streamed to.
type Manager struct {
	baseConfig Config
	sink       EventSink

	mu        sync.RWMutex
	instances map[string]*Sandbox
}

// NewManager creates a manager rooted at cfg.RootDir, publishing events to sink.
// A nil sink discards them, which is what a sandbox with nobody watching wants.
func NewManager(cfg Config, sink EventSink) (*Manager, error) {
	if cfg.RootDir == "" {
		return nil, errors.New("root directory cannot be empty")
	}

	if err := cfg.UIDRange.Validate(); err != nil {
		return nil, err
	}

	absRoot, err := filepath.Abs(cfg.RootDir)
	if err != nil {
		return nil, fmt.Errorf("resolve sandbox root: %w", err)
	}
	cfg.RootDir = absRoot

	return &Manager{
		baseConfig: cfg,
		sink:       sink,
		instances:  make(map[string]*Sandbox),
	}, nil
}

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

	// The id is settled before the sandbox exists, because creating the directory
	// is what claims it — and the claim has to be visible to the next agent that
	// asks for one.
	uid, err := m.assignUID(agentID, cfg.RootDir)
	if err != nil {
		return nil, fmt.Errorf("assign a user id to agent %s: %w", agentID, err)
	}

	sb, err := newSandbox(agentID, cfg, m.sink, uid)
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
