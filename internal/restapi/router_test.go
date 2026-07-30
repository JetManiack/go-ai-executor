package restapi_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/JetManiack/go-ai-executor/internal/humanauth"
	"github.com/JetManiack/go-ai-executor/internal/restapi"
	"github.com/JetManiack/go-ai-executor/internal/sandbox"
	"github.com/JetManiack/go-ai-executor/internal/storage"
)

func setupTestAPI(t *testing.T) (http.Handler, *storage.Actor) {
	t.Helper()
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}

	tempDir := t.TempDir()
	mgr, err := sandbox.NewManager(sandbox.Config{
		RootDir:        tempDir,
		DefaultTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("failed to create sandbox manager: %v", err)
	}

	authProvider, err := humanauth.NewStubProvider(db)
	if err != nil {
		t.Fatalf("failed to create stub auth provider: %v", err)
	}

	router := restapi.NewRouter(restapi.RouterOptions{
		DB:           db,
		Manager:      mgr,
		AuthProvider: authProvider,
	})

	agent, err := storage.CreateAgent(db, "TestAgent")
	if err != nil {
		t.Fatalf("failed to create agent: %v", err)
	}

	return router, agent
}

func TestAgentsEndpoints(t *testing.T) {
	router, agent := setupTestAPI(t)

	// 1. GET /agents
	req := httptest.NewRequest("GET", "/agents", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	// 2. POST /agents (create new agent)
	body, _ := json.Marshal(map[string]string{"display_name": "NewAgent"})
	req = httptest.NewRequest("POST", "/agents", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status 201 Created, got %d", rec.Code)
	}

	// 3. POST /agents/{id}/tokens (issue token)
	req = httptest.NewRequest("POST", "/agents/"+agent.ID+"/tokens", nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status 201 Created, got %d", rec.Code)
	}

	// 4. POST /agents/{id}/exec (manual command execution)
	execBody, _ := json.Marshal(map[string]string{"command": "echo 'ui exec test'"})
	req = httptest.NewRequest("POST", "/agents/"+agent.ID+"/exec", bytes.NewBuffer(execBody))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200 OK, got %d", rec.Code)
	}
}
