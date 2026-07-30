package mcpserver_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go-ai-executor/internal/mcpserver"
	"go-ai-executor/internal/sandbox"
	"go-ai-executor/internal/storage"
)

func TestRequireAuthToken(t *testing.T) {
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("failed to open storage: %v", err)
	}

	tempDir := t.TempDir()
	mgr, err := sandbox.NewManager(sandbox.Config{
		RootDir:        tempDir,
		DefaultTimeout: 5 * time.Second,
		MaxOutputBytes: 1024,
	})
	if err != nil {
		t.Fatalf("failed to create sandbox manager: %v", err)
	}

	agent, err := storage.CreateAgent(db, "TestAgent")
	if err != nil {
		t.Fatalf("failed to create agent: %v", err)
	}

	token, _, err := storage.IssueAgentToken(db, agent.ID)
	if err != nil {
		t.Fatalf("failed to issue agent token: %v", err)
	}

	handler := mcpserver.NewHTTPHandler(mgr, db)

	// 1. Request without auth header
	req := httptest.NewRequest("GET", "/mcp", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401 Unauthorized for missing auth header, got %d", rec.Code)
	}

	// 2. Request with invalid token
	req = httptest.NewRequest("GET", "/mcp", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401 Unauthorized for wrong token, got %d", rec.Code)
	}

	// 3. Request with valid token
	req = httptest.NewRequest("GET", "/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code == http.StatusUnauthorized {
		t.Errorf("expected auth check to pass, but got 401 Unauthorized")
	}
}
