package mcp_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"rhizome-mcp/internal/domain"
)

// TestCreateIssueReadyRankRoundTrip drives the Agent Board V0.1 READY-queue
// position end to end over MCP: create with a rank, read it back through the
// full detail view and the compact list view, reorder it, clear it, and reject
// an out-of-range value with a machine-actionable detail.
func TestCreateIssueReadyRankRoundTrip(t *testing.T) {
	ctx := context.Background()
	db, source := openDatabase(t, filepath.Join(t.TempDir(), "ready-rank.db"))
	defer db.Close(ctx)
	client, stop := newClient(t, composeServices(t, db, source))
	defer stop()

	created := call(t, client, "create_issue", map[string]any{
		"type": "task", "title": "queued", "status": "ready", "ready_rank": 7, "view": "full",
	})
	var createdIssue struct {
		Type      string `json:"type"`
		Status    string `json:"status"`
		ReadyRank *int64 `json:"ready_rank"`
		Version   int64  `json:"version"`
		ID        string `json:"id"`
	}
	decodeStructured(t, created, &createdIssue)
	if created.IsError || createdIssue.ID == "" || createdIssue.ReadyRank == nil || *createdIssue.ReadyRank != 7 {
		t.Fatalf("create_issue = %#v, issue = %#v", created, createdIssue)
	}

	listed := call(t, client, "list_issues", map[string]any{"statuses": []string{"ready"}})
	var list struct {
		Items []struct {
			ID        string `json:"id"`
			ReadyRank *int64 `json:"ready_rank"`
		} `json:"items"`
	}
	decodeStructured(t, listed, &list)
	if listed.IsError || len(list.Items) != 1 || list.Items[0].ReadyRank == nil || *list.Items[0].ReadyRank != 7 {
		t.Fatalf("list_issues = %#v, items = %#v", listed, list.Items)
	}

	updated := call(t, client, "update_issue", map[string]any{
		"issue_id": createdIssue.ID, "expected_version": createdIssue.Version,
		"changes": map[string]any{"ready_rank": 2}, "view": "full",
	})
	var updatedIssue struct {
		Issue struct {
			ReadyRank *int64 `json:"ready_rank"`
			Version   int64  `json:"version"`
		} `json:"issue"`
		ChangedFields []string `json:"changed_fields"`
	}
	decodeStructured(t, updated, &updatedIssue)
	if updated.IsError || updatedIssue.Issue.ReadyRank == nil || *updatedIssue.Issue.ReadyRank != 2 {
		t.Fatalf("update_issue = %#v, issue = %#v", updated, updatedIssue.Issue)
	}
	if !containsStringValue(updatedIssue.ChangedFields, "ready_rank") {
		t.Fatalf("changed_fields = %v, want ready_rank", updatedIssue.ChangedFields)
	}

	cleared := call(t, client, "update_issue", map[string]any{
		"issue_id": createdIssue.ID, "expected_version": updatedIssue.Issue.Version,
		"changes": map[string]any{"ready_rank": nil}, "view": "full",
	})
	var clearedIssue struct {
		Issue struct {
			ReadyRank *int64 `json:"ready_rank"`
		} `json:"issue"`
	}
	decodeStructured(t, cleared, &clearedIssue)
	if cleared.IsError || clearedIssue.Issue.ReadyRank != nil {
		t.Fatalf("cleared ready_rank = %#v", clearedIssue.Issue.ReadyRank)
	}

	// MCP advertises the documented 0..MaxReadyRank range in the input schema,
	// so an out-of-range value is refused before the handler runs. The domain
	// layer repeats the same bound for adapters that bypass the schema (CLI,
	// logical import), covered by the domain tests.
	for _, invalid := range []int64{-1, domain.MaxReadyRank + 1} {
		rejected := call(t, client, "create_issue", map[string]any{
			"type": "task", "title": "out of range", "status": "ready", "ready_rank": invalid,
		})
		if !rejected.IsError {
			t.Fatalf("create_issue with ready_rank %d was accepted", invalid)
		}
	}
}

// TestCreateAgentSessionRecordsWorktree proves create_agent_session accepts a
// worktree, normalizes it, and returns it in the session output, and that the
// persisted value survives a handle lookup. The read-back deliberately uses
// end_agent_session rather than get_project: get_project only ever reports the
// connection's own session, and explicit agent_session_handle lifecycle
// replaces connection-derived attribution, so its session field is null for a
// handle-based client (see adapter.go's projectOutputFor call sites).
func TestCreateAgentSessionRecordsWorktree(t *testing.T) {
	ctx := context.Background()
	db, source := openDatabase(t, filepath.Join(t.TempDir(), "session-worktree.db"))
	defer db.Close(ctx)
	client, stop := newClient(t, composeServices(t, db, source))
	defer stop()

	created := call(t, client, "create_agent_session", map[string]any{
		"client_name": "codex", "instance_key": "worker-1", "worktree": "  /tmp/wt/AB-1  ",
	})
	var sessionOutput struct {
		Session struct {
			ID          string  `json:"id"`
			InstanceKey *string `json:"instance_key"`
			Worktree    *string `json:"worktree"`
		} `json:"session"`
		Handle string `json:"agent_session_handle"`
	}
	decodeStructured(t, created, &sessionOutput)
	if created.IsError || sessionOutput.Session.Worktree == nil || *sessionOutput.Session.Worktree != "/tmp/wt/AB-1" {
		t.Fatalf("create_agent_session = %#v, session = %#v", created, sessionOutput.Session)
	}
	if sessionOutput.Session.InstanceKey == nil || *sessionOutput.Session.InstanceKey != "worker-1" {
		t.Fatalf("instance_key = %v, want worker-1", sessionOutput.Session.InstanceKey)
	}

	// end_agent_session re-reads the persisted row through the handle lookup, so
	// its output proves the worktree survives the storage round trip.
	ended := call(t, client, "end_agent_session", map[string]any{"agent_session_handle": sessionOutput.Handle})
	var endedOutput struct {
		Session struct {
			Worktree *string `json:"worktree"`
		} `json:"session"`
	}
	decodeStructured(t, ended, &endedOutput)
	if ended.IsError || endedOutput.Session.Worktree == nil ||
		*endedOutput.Session.Worktree != "/tmp/wt/AB-1" {
		t.Fatalf("end_agent_session = %#v, session = %#v", ended, endedOutput.Session)
	}

	rejected := call(t, client, "create_agent_session", map[string]any{
		"client_name": "codex", "worktree": "   ",
	})
	assertDomainError(t, rejected, domain.CodeInvalidArgument, false)

	// The advertised input schema carries the field, so clients can discover it.
	tools, err := client.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range tools.Tools {
		if tool.Name != "create_agent_session" {
			continue
		}
		data, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatal(err)
		}
		var schema struct {
			Properties map[string]json.RawMessage `json:"properties"`
		}
		if err := json.Unmarshal(data, &schema); err != nil {
			t.Fatal(err)
		}
		if _, ok := schema.Properties["worktree"]; !ok {
			t.Fatalf("create_agent_session schema properties = %v", schema.Properties)
		}
	}
}

func containsStringValue(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
