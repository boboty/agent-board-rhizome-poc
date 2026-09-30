//go:build integration

package integration_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// boardWriteHTTPClient drives the served board over real HTTP, observing
// redirects instead of following them so the POST/redirect/GET contract is
// asserted rather than hidden by the client.
type boardWriteHTTPClient struct {
	t      *testing.T
	base   string
	client *http.Client
}

func newBoardWriteHTTPClient(t *testing.T, endpoint string) *boardWriteHTTPClient {
	t.Helper()
	return &boardWriteHTTPClient{
		t:    t,
		base: strings.TrimSuffix(endpoint, "/"),
		client: &http.Client{
			Timeout:       integrationTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

func (client *boardWriteHTTPClient) do(request *http.Request) (int, string, string) {
	client.t.Helper()
	response, err := client.client.Do(request)
	if err != nil {
		client.t.Fatalf("%s %s: %v", request.Method, request.URL.Path, err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		client.t.Fatalf("read %s %s body: %v", request.Method, request.URL.Path, err)
	}
	return response.StatusCode, string(body), response.Header.Get("Location")
}

func (client *boardWriteHTTPClient) get(path string) (int, string) {
	client.t.Helper()
	status, body, _ := client.do(mustBoardRequest(client.t, http.MethodGet, client.base+path, ""))
	return status, body
}

func (client *boardWriteHTTPClient) postForm(path string, form url.Values, accept string) (int, string, string) {
	client.t.Helper()
	request := mustBoardRequest(client.t, http.MethodPost, client.base+path, form.Encode())
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if accept != "" {
		request.Header.Set("Accept", accept)
	}
	return client.do(request)
}

func mustBoardRequest(t *testing.T, method, target, body string) *http.Request {
	t.Helper()
	request, err := http.NewRequest(method, target, strings.NewReader(body))
	if err != nil {
		t.Fatalf("build %s %s: %v", method, target, err)
	}
	return request
}

var integrationCSRFPattern = regexp.MustCompile(`name="csrf_token" value="([^"]+)"`)

func (client *boardWriteHTTPClient) csrf() string {
	client.t.Helper()
	status, page := client.get("/")
	if status != http.StatusOK {
		client.t.Fatalf("GET / = %d", status)
	}
	match := integrationCSRFPattern.FindStringSubmatch(page)
	if len(match) != 2 || match[1] == "" {
		client.t.Fatalf("served board did not embed a CSRF token:\n%s", page)
	}
	return match[1]
}

type boardWriteWorkflowCard struct {
	IssueID        string `json:"issue_id"`
	IssueDisplayID string `json:"issue_display_id"`
	Title          string `json:"title"`
	Column         string `json:"column"`
	Priority       string `json:"priority"`
	ReadyRank      *int64 `json:"ready_rank"`
	Version        int64  `json:"version"`
}

type boardWriteWorkflowUnprojected struct {
	IssueID        string `json:"issue_id"`
	IssueDisplayID string `json:"issue_display_id"`
	Title          string `json:"title"`
	StoredStatus   string `json:"stored_status"`
	Reason         string `json:"reason"`
	Version        int64  `json:"version"`
}

type boardWriteWorkflow struct {
	Cards       []boardWriteWorkflowCard        `json:"cards"`
	Unprojected []boardWriteWorkflowUnprojected `json:"unprojected"`
}

func (workflow boardWriteWorkflow) cardByTitle(title string) (boardWriteWorkflowCard, bool) {
	for _, card := range workflow.Cards {
		if card.Title == title {
			return card, true
		}
	}
	return boardWriteWorkflowCard{}, false
}

func (client *boardWriteHTTPClient) workflow() boardWriteWorkflow {
	client.t.Helper()
	status, body := client.get("/api/board")
	if status != http.StatusOK {
		client.t.Fatalf("GET /api/board = %d", status)
	}
	var payload struct {
		Workflow boardWriteWorkflow `json:"workflow"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		client.t.Fatalf("decode board workflow: %v\n%s", err, body)
	}
	return payload.Workflow
}

func (workflow boardWriteWorkflow) cardByDisplayID(displayID string) (column, title, priority string, version int64, found bool) {
	for _, card := range workflow.Cards {
		if card.IssueDisplayID == displayID {
			return card.Column, card.Title, card.Priority, card.Version, true
		}
	}
	return "", "", "", 0, false
}

func (workflow boardWriteWorkflow) unprojectedByTitle(title string) (displayID, reason string, version int64, found bool) {
	for _, item := range workflow.Unprojected {
		if item.Title == title {
			return item.IssueDisplayID, item.Reason, item.Version, true
		}
	}
	return "", "", 0, false
}

func (workflow boardWriteWorkflow) readyColumnOrder() []string {
	order := make([]string, 0, len(workflow.Cards))
	for _, card := range workflow.Cards {
		if card.Column == "ready" {
			order = append(order, card.IssueDisplayID)
		}
	}
	return order
}

// TestIntegrationBoardWriteLoopOverHTTP is acceptance AB-4 #11 end to end
// against the real binary, real SQLite, and the real HTTP server:
// create -> edit -> move to READY -> reorder -> refresh, plus the optimistic
// conflict and the CSRF/origin refusals.
func TestIntegrationBoardWriteLoopOverHTTP(t *testing.T) {
	t.Parallel()
	env := newIntegrationEnvironment(t)
	session := env.connect(t)

	server := launchIntegrationBoardServer(t, env, "127.0.0.1:0")
	t.Cleanup(func() { stopIntegrationBoardServer(t, server) })
	client := newBoardWriteHTTPClient(t, server.waitForEndpoint(t))

	// Acceptance #8: the read side is untouched.
	status, page := client.get("/")
	if status != http.StatusOK || !strings.Contains(page, "+ New Task") {
		t.Fatalf("GET / = %d, page missing the board or the create form", status)
	}
	if status, _, _ := client.do(mustBoardRequest(t, http.MethodHead, client.base+"/", "")); status != http.StatusOK {
		t.Fatalf("HEAD / = %d", status)
	}
	csrf := client.csrf()

	// Acceptance #1: create from the board, defaulting to open.
	status, _, location := client.postForm("/api/issues", url.Values{
		"csrf_token": {csrf}, "title": {"AB-4 write loop task"}, "description": {"first description"},
		"acceptance_criteria": {"accepts a written task"}, "priority": {"high"}, "status": {"open"},
	}, "")
	if status != http.StatusSeeOther || location != "/?notice=created" {
		t.Fatalf("create = %d %q", status, location)
	}
	workflow := client.workflow()
	displayID, reason, version, found := workflow.unprojectedByTitle("AB-4 write loop task")
	if !found || reason != "not_ready" {
		t.Fatalf("created task is not an unprojected open task: found=%v reason=%q", found, reason)
	}
	if status, page := client.get("/"); status != http.StatusOK || !strings.Contains(page, "AB-4 write loop task") {
		t.Fatalf("created task did not survive a refresh: %d", status)
	}

	// Acceptance #2: edit title/description/acceptance criteria/priority.
	status, _, location = client.postForm("/api/issues/"+displayID, url.Values{
		"csrf_token": {csrf}, "expected_version": {fmt.Sprint(version)},
		"title": {"AB-4 write loop task edited"}, "description": {"second description"},
		"acceptance_criteria": {"accepts an edited task"}, "priority": {"low"},
	}, "")
	if status != http.StatusSeeOther || location != "/?notice=updated" {
		t.Fatalf("edit = %d %q", status, location)
	}
	status, detailPage := client.get("/issues/" + displayID)
	if status != http.StatusOK || !strings.Contains(detailPage, "second description") ||
		!strings.Contains(detailPage, "accepts an edited task") {
		t.Fatalf("edited bodies were not persisted (status %d)", status)
	}
	workflow = client.workflow()
	if _, title, _, _, found := workflow.cardByDisplayID(displayID); found {
		t.Fatalf("edited task should still be unprojected, found as card %q", title)
	}
	_, _, unprojectedVersion, _ := workflow.unprojectedByTitle("AB-4 write loop task edited")
	if unprojectedVersion <= version {
		t.Fatalf("edit did not advance the version: %d -> %d", version, unprojectedVersion)
	}

	// Acceptance #3: queue the open task into READY through the update path.
	status, _, location = client.postForm("/api/issues/"+displayID+"/ready", url.Values{
		"csrf_token": {csrf}, "expected_version": {fmt.Sprint(unprojectedVersion)},
	}, "")
	if status != http.StatusSeeOther || location != "/?notice=queued" {
		t.Fatalf("queue = %d %q", status, location)
	}
	column, title, priority, firstVersion, found := client.workflow().cardByDisplayID(displayID)
	if !found || column != "ready" || title != "AB-4 write loop task edited" || priority != "low" {
		t.Fatalf("queued card = %q/%q/%q found=%v", column, title, priority, found)
	}

	// A second READY task, created directly in READY with an explicit rank.
	status, _, location = client.postForm("/api/issues", url.Values{
		"csrf_token": {csrf}, "title": {"AB-4 second queue task"}, "priority": {"medium"},
		"status": {"ready"}, "ready_rank": {"100"},
	}, "")
	if status != http.StatusSeeOther || location != "/?notice=created" {
		t.Fatalf("second create = %d %q", status, location)
	}
	var secondDisplayID string
	for _, card := range client.workflow().Cards {
		if card.Title == "AB-4 second queue task" {
			secondDisplayID = card.IssueDisplayID
		}
	}
	if secondDisplayID == "" {
		t.Fatal("second READY task missing from the board")
	}

	// Acceptance #4: the first task is unranked, so it currently sits behind the
	// ranked second task. Move it up, then refresh and check the order stuck.
	before := client.workflow().readyColumnOrder()
	if len(before) != 2 || before[0] != secondDisplayID || before[1] != displayID {
		t.Fatalf("READY order before reorder = %v, want [%s %s]", before, secondDisplayID, displayID)
	}
	status, _, location = client.postForm("/api/issues/"+displayID+"/rank", url.Values{
		"csrf_token": {csrf}, "expected_version": {fmt.Sprint(firstVersion)}, "direction": {"up"},
	}, "")
	if status != http.StatusSeeOther || location != "/?notice=reordered" {
		t.Fatalf("reorder = %d %q", status, location)
	}
	after := client.workflow().readyColumnOrder()
	if len(after) != 2 || after[0] != displayID || after[1] != secondDisplayID {
		t.Fatalf("READY order after reorder = %v, want [%s %s]", after, displayID, secondDisplayID)
	}
	_, refreshedPage := client.get("/")
	firstIndex := strings.Index(refreshedPage, displayID)
	secondIndex := strings.Index(refreshedPage, secondDisplayID)
	if firstIndex < 0 || secondIndex < 0 || firstIndex > secondIndex {
		t.Fatalf("refreshed page did not render the persisted READY order (first at %d, second at %d)", firstIndex, secondIndex)
	}

	// Acceptance #6: a stale edit is refused, reported, and never overwrites.
	// The reorder above renumbered this card, so the version captured before it
	// is now stale.
	status, _, location = client.postForm("/api/issues/"+displayID, url.Values{
		"csrf_token": {csrf}, "expected_version": {fmt.Sprint(firstVersion)},
		"title": {"AB-4 stale overwrite"}, "description": {"stale"}, "priority": {"critical"},
	}, "")
	if status != http.StatusSeeOther || location != "/?error=version_conflict" {
		t.Fatalf("stale edit = %d %q", status, location)
	}
	status, body, _ := client.postForm("/api/issues/"+displayID, url.Values{
		"csrf_token": {csrf}, "expected_version": {fmt.Sprint(firstVersion)},
		"title": {"AB-4 stale overwrite"}, "description": {"stale"}, "priority": {"critical"},
	}, "application/json")
	if status != http.StatusConflict || !strings.Contains(body, "version_conflict") {
		t.Fatalf("stale edit JSON = %d %s", status, body)
	}
	_, currentTitle, _, _, _ := client.workflow().cardByDisplayID(displayID)
	if currentTitle != "AB-4 write loop task edited" {
		t.Fatalf("stale edit overwrote the task: %q", currentTitle)
	}

	// Acceptance #7: a write without the token, cross-site, or cross-origin is
	// refused and changes nothing.
	for _, name := range []string{"missing csrf", "wrong csrf"} {
		token := ""
		if name == "wrong csrf" {
			token = "not-the-served-token"
		}
		status, _, _ = client.postForm("/api/issues", url.Values{
			"csrf_token": {token}, "title": {"AB-4 rejected"},
		}, "")
		if status != http.StatusForbidden {
			t.Fatalf("%s = %d, want 403", name, status)
		}
	}
	crossSite := mustBoardRequest(t, http.MethodPost, client.base+"/api/issues", url.Values{
		"csrf_token": {csrf}, "title": {"AB-4 rejected"},
	}.Encode())
	crossSite.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	crossSite.Header.Set("Sec-Fetch-Site", "cross-site")
	if status, _, _ = client.do(crossSite); status != http.StatusForbidden {
		t.Fatalf("cross-site write = %d, want 403", status)
	}
	crossOrigin := mustBoardRequest(t, http.MethodPost, client.base+"/api/issues", url.Values{
		"csrf_token": {csrf}, "title": {"AB-4 rejected"},
	}.Encode())
	crossOrigin.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	crossOrigin.Header.Set("Origin", "http://evil.example")
	if status, _, _ = client.do(crossOrigin); status != http.StatusForbidden {
		t.Fatalf("cross-origin write = %d, want 403", status)
	}
	for _, card := range client.workflow().Cards {
		if card.Title == "AB-4 rejected" {
			t.Fatal("a rejected write was applied")
		}
	}

	// Acceptance #5, projection half: a stored-ready task held by an active
	// attempt is displayed as IN PROGRESS, so it is not in the READY column the
	// operator reorders. Planning from stored status would silently move it.
	claimed := mustCreateBoardIssue(t, session, map[string]any{
		"type": "task", "title": "AB-4 claimed READY task", "status": "ready",
	})
	claim := callIntegrationTool(t, session, "claim_issue", map[string]any{
		"issue_id": claimed.DisplayID, "lease_seconds": 600,
	})
	var claimOutput struct {
		Attempt struct {
			ID string `json:"id"`
		} `json:"attempt"`
	}
	decodeIntegrationResult(t, claim, &claimOutput)
	if claim.IsError || claimOutput.Attempt.ID == "" {
		t.Fatalf("claim_issue result = %#v, decoded = %#v", claim, claimOutput)
	}
	hidden, found := client.workflow().cardByTitle("AB-4 claimed READY task")
	if !found || hidden.Column != "in_progress" {
		t.Fatalf("claimed task column = %q found=%v, want in_progress", hidden.Column, found)
	}
	visibleBefore := client.workflow().readyColumnOrder()
	if len(visibleBefore) != 2 || visibleBefore[0] != displayID || visibleBefore[1] != secondDisplayID {
		t.Fatalf("READY column = %v, want the two visible cards only", visibleBefore)
	}
	firstCard, found := client.workflow().cardByTitle("AB-4 write loop task edited")
	if !found {
		t.Fatal("visible first READY card missing")
	}
	status, _, location = client.postForm("/api/issues/"+displayID+"/rank", url.Values{
		"csrf_token": {csrf}, "expected_version": {fmt.Sprint(firstCard.Version)}, "direction": {"down"},
	}, "")
	if status != http.StatusSeeOther || location != "/?notice=reordered" {
		t.Fatalf("reorder with a hidden stored-ready neighbour = %d %q", status, location)
	}
	visibleAfter := client.workflow().readyColumnOrder()
	if len(visibleAfter) != 2 || visibleAfter[0] != secondDisplayID || visibleAfter[1] != displayID {
		t.Fatalf("visible READY order = %v, want [%s %s]", visibleAfter, secondDisplayID, displayID)
	}
	hiddenAfter, found := client.workflow().cardByTitle("AB-4 claimed READY task")
	if !found || hiddenAfter.Column != "in_progress" {
		t.Fatalf("claimed task left IN PROGRESS: %#v found=%v", hiddenAfter, found)
	}
	if hiddenAfter.Version != hidden.Version || hiddenAfter.ReadyRank != nil {
		t.Fatalf("an invisible stored-ready task was rewritten: %#v (was %#v)", hiddenAfter, hidden)
	}
}
