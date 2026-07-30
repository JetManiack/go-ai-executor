package mcpserver

import (
	"context"
	"strings"
	"testing"

	"gorm.io/gorm"

	"github.com/JetManiack/go-ai-executor/internal/storage"
)

// mustBlock blocks agentID's sandbox on behalf of a freshly-provisioned admin.
func mustBlock(t *testing.T, db *gorm.DB, agentID, reason string) *storage.SandboxBlock {
	t.Helper()
	admin, err := storage.GetOrCreateHumanActor(db, "admin-subject", "Grace", "admin")
	if err != nil {
		t.Fatalf("GetOrCreateHumanActor: %v", err)
	}
	block, err := storage.BlockSandbox(db, agentID, admin, reason)
	if err != nil {
		t.Fatalf("BlockSandbox: %v", err)
	}
	return block
}

// TestBlockedSandboxRefusesEveryTool is the enforcement half of the emergency
// stop: killing the running processes is pointless if the next call starts more.
func TestBlockedSandboxRefusesEveryTool(t *testing.T) {
	db := openTestDB(t)
	agent, token := mustAgentWithToken(t, db, "runaway")
	session := connectSession(t, newTestServer(t, testDeps(t, db)), token)

	// Write a file before the block, so read_file has something it would
	// otherwise succeed at.
	if res := callTool(t, session, "write_file", map[string]any{"path": "a.txt", "content": "x"}); res.IsError {
		t.Fatalf("write_file before block: %s", contentText(res.Content))
	}

	mustBlock(t, db, agent.ID, "spawning processes in a loop")

	calls := []struct {
		tool string
		args map[string]any
	}{
		{"exec_command", map[string]any{"command": "echo still running"}},
		{"read_file", map[string]any{"path": "a.txt"}},
		{"write_file", map[string]any{"path": "b.txt", "content": "x"}},
		{"list_dir", map[string]any{}},
		{"delete_file", map[string]any{"path": "a.txt"}},
		{"get_sandbox_status", map[string]any{}},
	}

	for _, call := range calls {
		t.Run(call.tool, func(t *testing.T) {
			res := callTool(t, session, call.tool, call.args)
			if !res.IsError {
				t.Fatalf("%s succeeded on a blocked sandbox", call.tool)
			}

			message := contentText(res.Content)
			// The agent has to be able to tell an operator decision from a
			// transient failure, or it will simply retry the block forever.
			for _, want := range []string{"administratively blocked", "Grace", "spawning processes in a loop", "do not retry"} {
				if !strings.Contains(message, want) {
					t.Errorf("error message %q does not mention %q", message, want)
				}
			}
		})
	}
}

func TestReleasedSandboxResumes(t *testing.T) {
	db := openTestDB(t)
	agent, token := mustAgentWithToken(t, db, "runaway")
	session := connectSession(t, newTestServer(t, testDeps(t, db)), token)

	mustBlock(t, db, agent.ID, "just checking")
	if res := callTool(t, session, "list_dir", map[string]any{}); !res.IsError {
		t.Fatal("list_dir succeeded while blocked")
	}

	admin, err := storage.GetOrCreateHumanActor(db, "admin-subject", "Grace", "admin")
	if err != nil {
		t.Fatalf("GetOrCreateHumanActor: %v", err)
	}
	if err := storage.ReleaseSandbox(db, agent.ID, admin); err != nil {
		t.Fatalf("ReleaseSandbox: %v", err)
	}

	if res := callTool(t, session, "list_dir", map[string]any{}); res.IsError {
		t.Errorf("list_dir still refused after release: %s", contentText(res.Content))
	}
}

// TestBlockIsReadPerCallNotCached covers the decision to hit the database on
// every tool call: a block applied by another replica has to take effect on the
// next call, without this process being told about it.
func TestBlockIsReadPerCallNotCached(t *testing.T) {
	db := openTestDB(t)
	agent, token := mustAgentWithToken(t, db, "agent-1")
	session := connectSession(t, newTestServer(t, testDeps(t, db)), token)

	if res := callTool(t, session, "list_dir", map[string]any{}); res.IsError {
		t.Fatalf("list_dir before block: %s", contentText(res.Content))
	}

	// Written straight to the database, as a second replica would.
	mustBlock(t, db, agent.ID, "blocked elsewhere")

	if res := callTool(t, session, "list_dir", map[string]any{}); !res.IsError {
		t.Error("an established session kept working after the sandbox was blocked in the database")
	}
}

// TestUnreadableBlockStateFailsClosed checks the direction the code errs in when
// it cannot tell whether a sandbox is blocked.
func TestUnreadableBlockStateFailsClosed(t *testing.T) {
	db := openTestDB(t)
	agent := mustAgent(t, db, "agent-1")
	deps := testDeps(t, db)

	// Drop the table the block check reads, so the query fails rather than
	// returning "no block".
	if err := db.Migrator().DropTable(&storage.SandboxBlock{}); err != nil {
		t.Fatalf("drop sandbox_blocks: %v", err)
	}

	ctx := WithActorForTesting(context.Background(), agent)
	if _, _, err := listDirHandler(deps)(ctx, nil, ListDirInput{}); err == nil {
		t.Error("list_dir succeeded although block state could not be verified")
	}
}
