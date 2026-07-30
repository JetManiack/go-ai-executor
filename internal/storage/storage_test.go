package storage_test

import (
	"testing"

	"gorm.io/gorm"

	"github.com/JetManiack/go-ai-executor/internal/storage"
)

func setupTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("failed to open in-memory db: %v", err)
	}
	return db
}

func TestAgentTokenLifecycle(t *testing.T) {
	db := setupTestDB(t)

	// 1. Create agent
	agent, err := storage.CreateAgent(db, "TestAgent")
	if err != nil {
		t.Fatalf("failed to create agent: %v", err)
	}
	if agent.DisplayName != "TestAgent" || agent.Kind != storage.ActorKindAgent {
		t.Fatalf("unexpected agent: %+v", agent)
	}

	// 2. Issue token
	rawToken, cred, err := storage.IssueAgentToken(db, agent.ID)
	if err != nil {
		t.Fatalf("failed to issue token: %v", err)
	}
	if rawToken == "" || cred.ActorID != agent.ID {
		t.Fatalf("unexpected token cred: %+v", cred)
	}

	// 3. Authenticate valid token
	authenticated, err := storage.AuthenticateAgentToken(db, rawToken)
	if err != nil {
		t.Fatalf("AuthenticateAgentToken failed: %v", err)
	}
	if authenticated.ID != agent.ID {
		t.Errorf("authenticated actor mismatch: expected %s, got %s", agent.ID, authenticated.ID)
	}

	// 4. Revoke token
	if err := storage.RevokeAgentToken(db, cred.ID); err != nil {
		t.Fatalf("RevokeAgentToken failed: %v", err)
	}

	// 5. Authenticate revoked token should fail
	_, err = storage.AuthenticateAgentToken(db, rawToken)
	if err != storage.ErrTokenRevoked {
		t.Errorf("expected ErrTokenRevoked, got: %v", err)
	}
}

func TestExecLogPersistence(t *testing.T) {
	db := setupTestDB(t)
	agent, _ := storage.CreateAgent(db, "LogAgent")

	log := &storage.ExecLog{
		AgentID:    agent.ID,
		Command:    "echo 123",
		Stdout:     "123\n",
		ExitCode:   0,
		DurationMs: 5,
	}
	if err := storage.RecordExecLog(db, log); err != nil {
		t.Fatalf("RecordExecLog failed: %v", err)
	}

	logs, err := storage.ListExecLogs(db, agent.ID, 10)
	if err != nil {
		t.Fatalf("ListExecLogs failed: %v", err)
	}
	if len(logs) != 1 || logs[0].Command != "echo 123" {
		t.Errorf("unexpected logs: %+v", logs)
	}
}
