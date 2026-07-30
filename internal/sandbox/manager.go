package sandbox

import (
	"fmt"
	"path/filepath"
	"sync"
)

type Manager struct {
	baseConfig  Config
	broadcaster *Broadcaster
	mu          sync.RWMutex
	instances   map[string]*Sandbox
}

func NewManager(cfg Config) (*Manager, error) {
	if cfg.RootDir == "" {
		return nil, fmt.Errorf("root directory cannot be empty")
	}

	absRoot, err := filepath.Abs(cfg.RootDir)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve absolute path for root directory: %w", err)
	}

	cfg.RootDir = absRoot

	return &Manager{
		baseConfig:  cfg,
		broadcaster: NewBroadcaster(),
		instances:   make(map[string]*Sandbox),
	}, nil
}

// Broadcaster returns the manager's event bus, which fans sandbox output out
// to the humans watching a terminal.
func (m *Manager) Broadcaster() *Broadcaster {
	return m.broadcaster
}

func (m *Manager) GetSandbox(agentID string) (*Sandbox, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if sb, ok := m.instances[agentID]; ok {
		return sb, nil
	}

	var agentRootDir string
	if agentID == "" || agentID == "default" {
		agentRootDir = m.baseConfig.RootDir
	} else {
		agentRootDir = filepath.Join(m.baseConfig.RootDir, "agents", agentID)
	}

	cfg := m.baseConfig
	cfg.RootDir = agentRootDir

	sb, err := New(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create sandbox for agent %s: %w", agentID, err)
	}

	m.instances[agentID] = sb
	return sb, nil
}
