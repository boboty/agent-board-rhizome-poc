//go:build integration

package integration_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestIntegrationBoardShowsActiveExecutorRuntimeInfo locks in acceptance
// AB-2 #1-#4 end to end through the real binary: a session created with full
// runtime metadata claims an issue, and `board` reports that metadata for the
// active attempt in table, JSON, and HTML output; a second attempt claimed
// without a session handle stays on the board with the fields absent; and
// finishing the attributed attempt removes it from the current-executor list.
func TestIntegrationBoardShowsActiveExecutorRuntimeInfo(t *testing.T) {
	t.Parallel()
	env := newIntegrationEnvironment(t)
	session := env.connect(t)

	createdSession := callIntegrationTool(t, session, "create_agent_session", map[string]any{
		"client_name":  "Codex CLI",
		"agent_label":  "Luna",
		"model":        "gpt-5",
		"instance_key": "worker-1",
		"worktree":     "/tmp/wt/AB-2",
	})
	var sessionOutput struct {
		Handle string `json:"agent_session_handle"`
	}
	decodeIntegrationResult(t, createdSession, &sessionOutput)
	if createdSession.IsError || sessionOutput.Handle == "" {
		t.Fatalf("create_agent_session result = %#v, decoded = %#v", createdSession, sessionOutput)
	}

	attributedIssue := mustCreateBoardIssue(t, session, map[string]any{
		"type": "task", "title": "Attributed board work", "status": "ready",
	})
	claimed := callIntegrationTool(t, session, "claim_issue", map[string]any{
		"issue_id":             attributedIssue.DisplayID,
		"lease_seconds":        600,
		"agent_session_handle": sessionOutput.Handle,
	})
	var claim struct {
		Attempt struct {
			ID string `json:"id"`
		} `json:"attempt"`
		LeaseToken string `json:"lease_token"`
	}
	decodeIntegrationResult(t, claimed, &claim)
	if claimed.IsError || claim.Attempt.ID == "" || claim.LeaseToken == "" {
		t.Fatalf("claim_issue result = %#v, decoded = %#v", claimed, claim)
	}

	// A second, session-less claim proves degradation in the same projection.
	sessionlessIssue := mustCreateBoardIssue(t, session, map[string]any{
		"type": "task", "title": "Session-less board work", "status": "ready",
	})
	sessionlessClaim := callIntegrationTool(t, session, "claim_issue", map[string]any{
		"issue_id":      sessionlessIssue.DisplayID,
		"lease_seconds": 600,
	})
	var sessionless struct {
		Attempt struct {
			ID string `json:"id"`
		} `json:"attempt"`
	}
	decodeIntegrationResult(t, sessionlessClaim, &sessionless)
	if sessionlessClaim.IsError || sessionless.Attempt.ID == "" {
		t.Fatalf("session-less claim_issue result = %#v", sessionlessClaim)
	}

	type boardAttempt struct {
		AttemptID          string     `json:"attempt_id"`
		IssueDisplayID     string     `json:"issue_display_id"`
		SessionLabel       *string    `json:"session_label"`
		SessionInstanceKey *string    `json:"session_instance_key"`
		SessionClientName  *string    `json:"session_client_name"`
		SessionModel       *string    `json:"session_model"`
		SessionWorktree    *string    `json:"session_worktree"`
		LeaseExpiresAt     *time.Time `json:"lease_expires_at"`
	}
	decodeBoard := func(t *testing.T) ([]byte, []boardAttempt) {
		t.Helper()
		output := runIntegrationCommand(t, env, "--data-root", env.dataRoot, "board", "--format", "json")
		var payload struct {
			ActiveAttempts []boardAttempt `json:"active_attempts"`
		}
		if err := json.Unmarshal(output, &payload); err != nil {
			t.Fatalf("decode board --format json: %v\noutput:\n%s", err, output)
		}
		return output, payload.ActiveAttempts
	}

	jsonOutput, attempts := decodeBoard(t)
	if len(attempts) != 2 {
		t.Fatalf("active_attempts = %#v, want both claims", attempts)
	}
	var attributed *boardAttempt
	for index := range attempts {
		if attempts[index].AttemptID == claim.Attempt.ID {
			attributed = &attempts[index]
		}
	}
	if attributed == nil {
		t.Fatalf("attributed attempt %s missing from active_attempts: %#v", claim.Attempt.ID, attempts)
	}
	if attributed.SessionLabel == nil || *attributed.SessionLabel != "Luna" ||
		attributed.SessionInstanceKey == nil || *attributed.SessionInstanceKey != "worker-1" ||
		attributed.SessionClientName == nil || *attributed.SessionClientName != "Codex CLI" ||
		attributed.SessionModel == nil || *attributed.SessionModel != "gpt-5" ||
		attributed.SessionWorktree == nil || *attributed.SessionWorktree != "/tmp/wt/AB-2" {
		t.Fatalf("board JSON runtime info = %#v", attributed)
	}
	if attributed.LeaseExpiresAt == nil || !attributed.LeaseExpiresAt.After(time.Now()) {
		t.Fatalf("board JSON lease expiry = %v, want a future timestamp", attributed.LeaseExpiresAt)
	}
	for _, key := range []string{"session_instance_key", "session_client_name", "session_model", "session_worktree"} {
		if !strings.Contains(string(jsonOutput), `"`+key+`"`) {
			t.Fatalf("board JSON is missing %q:\n%s", key, jsonOutput)
		}
	}
	for _, attempt := range attempts {
		if attempt.AttemptID != sessionless.Attempt.ID {
			continue
		}
		if attempt.SessionLabel != nil || attempt.SessionInstanceKey != nil || attempt.SessionClientName != nil ||
			attempt.SessionModel != nil || attempt.SessionWorktree != nil {
			t.Fatalf("session-less attempt exposed runtime info: %#v", attempt)
		}
	}

	// Table output carries the same values under the same column names.
	tableOutput := string(runIntegrationCommand(t, env, "--data-root", env.dataRoot, "board", "--format", "table"))
	for _, want := range []string{
		"attempt_id\tissue\tkind\tsession_label\tsession_instance_key\tsession_client_name\tsession_model\tsession_worktree\tlease_expires_at",
		claim.Attempt.ID + "\t" + attributedIssue.DisplayID + "\twork\tLuna\tworker-1\tCodex CLI\tgpt-5\t/tmp/wt/AB-2\t",
	} {
		if !strings.Contains(tableOutput, want) {
			t.Fatalf("board table output is missing %q:\n%s", want, tableOutput)
		}
	}

	// HTML output (self-contained snapshot) shows the same information.
	htmlPath := filepath.Join(t.TempDir(), "board.html")
	runIntegrationCommand(t, env, "--data-root", env.dataRoot, "board", "--output", htmlPath)
	htmlBytes, err := os.ReadFile(htmlPath)
	if err != nil {
		t.Fatalf("read board HTML output: %v", err)
	}
	htmlOutput := string(htmlBytes)
	for _, want := range []string{"Instance", "worker-1", "Codex CLI", "gpt-5", "/tmp/wt/AB-2"} {
		if !strings.Contains(htmlOutput, want) {
			t.Fatalf("board HTML output is missing %q:\n%s", want, htmlOutput)
		}
	}

	// Finishing the attributed attempt removes it as a current executor; the
	// session-less claim is unaffected.
	finished := callIntegrationTool(t, session, "finish_attempt", map[string]any{
		"attempt_id": claim.Attempt.ID, "lease_token": claim.LeaseToken,
		"outcome": "completed", "result_summary": "done",
		"target_issue_status": "ready",
	})
	if finished.IsError {
		t.Fatalf("finish_attempt result = %#v", finished)
	}
	_, attempts = decodeBoard(t)
	if len(attempts) != 1 || attempts[0].AttemptID != sessionless.Attempt.ID {
		t.Fatalf("active_attempts after finishing = %#v, want only the session-less claim", attempts)
	}
}
