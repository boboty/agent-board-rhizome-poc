package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"rhizome-mcp/internal/application"
	"rhizome-mcp/internal/domain"
)

// stubBoardWriteService records the application inputs the HTTP layer builds,
// so the tests prove the handler maps form fields onto the existing use cases
// instead of reaching into storage.
type stubBoardWriteService struct {
	issue     domain.Issue
	createErr error
	updateErr error
	queueErr  error
	moveErr   error

	creates []application.CreateBoardTaskInput
	updates []application.UpdateBoardTaskInput
	queues  []application.MoveBoardTaskToReadyInput
	moves   []application.MoveBoardReadyTaskInput
}

func (stub *stubBoardWriteService) CreateTask(_ context.Context, input application.CreateBoardTaskInput) (application.CreateBoardTaskResult, error) {
	stub.creates = append(stub.creates, input)
	if stub.createErr != nil {
		return application.CreateBoardTaskResult{}, stub.createErr
	}
	return application.CreateBoardTaskResult{Issue: stub.issue}, nil
}

func (stub *stubBoardWriteService) UpdateTask(_ context.Context, input application.UpdateBoardTaskInput) (application.UpdateBoardTaskResult, error) {
	stub.updates = append(stub.updates, input)
	if stub.updateErr != nil {
		return application.UpdateBoardTaskResult{}, stub.updateErr
	}
	return application.UpdateBoardTaskResult{Issue: stub.issue}, nil
}

func (stub *stubBoardWriteService) MoveTaskToReady(_ context.Context, input application.MoveBoardTaskToReadyInput) (application.UpdateBoardTaskResult, error) {
	stub.queues = append(stub.queues, input)
	if stub.queueErr != nil {
		return application.UpdateBoardTaskResult{}, stub.queueErr
	}
	return application.UpdateBoardTaskResult{Issue: stub.issue}, nil
}

func (stub *stubBoardWriteService) MoveReadyTask(_ context.Context, input application.MoveBoardReadyTaskInput) (application.MoveBoardReadyTaskResult, error) {
	stub.moves = append(stub.moves, input)
	if stub.moveErr != nil {
		return application.MoveBoardReadyTaskResult{}, stub.moveErr
	}
	return application.MoveBoardReadyTaskResult{Issue: stub.issue, Updates: 1}, nil
}

var boardCSRFTokenPattern = regexp.MustCompile(`name="csrf_token" value="([^"]+)"`)

// newWritableBoardHandler returns a handler plus the CSRF token it embedded in
// the served page, which is also the proof that the page carries the forms.
func newWritableBoardHandler(t *testing.T, writes *stubBoardWriteService) (http.Handler, string) {
	t.Helper()
	handler := NewWritableBoardHTTPHandler(&stubBoardService{board: boardResultFixture()}, writes)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET / status = %d, want %d", recorder.Code, http.StatusOK)
	}
	match := boardCSRFTokenPattern.FindStringSubmatch(recorder.Body.String())
	if len(match) != 2 || match[1] == "" {
		t.Fatalf("served page did not embed a CSRF token:\n%s", recorder.Body.String())
	}
	return handler, match[1]
}

func boardWriteRequest(t *testing.T, target string, form url.Values, token string, jsonResponse bool) *http.Request {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, target, strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Origin", "http://example.com")
	if token != "" {
		request.Header.Set("X-CSRF-Token", token)
	}
	if jsonResponse {
		request.Header.Set("Accept", "application/json")
	}
	return request
}

func TestBoardWriteCreatesEditsQueuesAndReorders(t *testing.T) {
	writes := &stubBoardWriteService{issue: domain.Issue{ID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", DisplayID: "ISSUE-1", Type: domain.TypeTask, Title: "Created", Status: domain.StatusOpen, Priority: domain.PriorityMedium, Version: 1}}
	handler, token := newWritableBoardHandler(t, writes)

	// Create.
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, boardWriteRequest(t, "/api/issues", url.Values{
		"csrf_token": {token}, "title": {"  Ship it  "}, "description": {"why"},
		"acceptance_criteria": {"when"}, "priority": {"high"}, "status": {"ready"}, "ready_rank": {"30"},
	}, token, true))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if len(writes.creates) != 1 {
		t.Fatalf("create inputs = %#v", writes.creates)
	}
	created := writes.creates[0]
	if created.Fields.Title != "Ship it" || created.Fields.Description == nil || *created.Fields.Description != "why" ||
		created.Fields.AcceptanceCriteria == nil || *created.Fields.AcceptanceCriteria != "when" ||
		created.Fields.Priority != domain.PriorityHigh || created.Status != domain.StatusReady ||
		created.ReadyRank == nil || *created.ReadyRank != 30 {
		t.Fatalf("create input = %#v", created)
	}
	var createPayload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &createPayload); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if createPayload["display_id"] != "ISSUE-1" || createPayload["status"] != "open" {
		t.Fatalf("create payload = %#v", createPayload)
	}

	// Edit.
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, boardWriteRequest(t, "/api/issues/ISSUE-1", url.Values{
		"csrf_token": {token}, "expected_version": {"4"}, "title": {"Edited"},
		"description": {""}, "acceptance_criteria": {"new criteria"}, "priority": {"low"},
	}, token, true))
	if recorder.Code != http.StatusOK {
		t.Fatalf("edit status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if len(writes.updates) != 1 {
		t.Fatalf("update inputs = %#v", writes.updates)
	}
	update := writes.updates[0]
	if update.IssueID != "ISSUE-1" || update.ExpectedVersion != 4 || update.Fields.Title != "Edited" ||
		update.Fields.Description != nil || update.Fields.AcceptanceCriteria == nil ||
		*update.Fields.AcceptanceCriteria != "new criteria" || update.Fields.Priority != domain.PriorityLow {
		t.Fatalf("update input = %#v", update)
	}

	// Queue into READY.
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, boardWriteRequest(t, "/api/issues/ISSUE-1/ready", url.Values{
		"csrf_token": {token}, "expected_version": {"4"}, "ready_rank": {"15"},
	}, token, true))
	if recorder.Code != http.StatusOK {
		t.Fatalf("ready status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if len(writes.queues) != 1 || writes.queues[0].IssueID != "ISSUE-1" || writes.queues[0].ExpectedVersion != 4 ||
		writes.queues[0].ReadyRank == nil || *writes.queues[0].ReadyRank != 15 {
		t.Fatalf("queue input = %#v", writes.queues)
	}

	// Reorder.
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, boardWriteRequest(t, "/api/issues/ISSUE-1/rank", url.Values{
		"csrf_token": {token}, "expected_version": {"5"}, "direction": {"up"},
	}, token, true))
	if recorder.Code != http.StatusOK {
		t.Fatalf("rank status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if len(writes.moves) != 1 || writes.moves[0].IssueID != "ISSUE-1" || writes.moves[0].ExpectedVersion != 5 ||
		writes.moves[0].Direction != application.BoardReadyMoveUp {
		t.Fatalf("move inputs = %#v", writes.moves)
	}
}

func TestBoardWriteRequiresCSRFAndSameOrigin(t *testing.T) {
	writes := &stubBoardWriteService{issue: domain.Issue{ID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", DisplayID: "ISSUE-1"}}
	handler, token := newWritableBoardHandler(t, writes)
	form := url.Values{"title": {"Task"}}

	tests := []struct {
		name    string
		request func() *http.Request
		status  int
	}{
		{
			name: "missing csrf token",
			request: func() *http.Request {
				return boardWriteRequest(t, "/api/issues", form, "", true)
			},
			status: http.StatusForbidden,
		},
		{
			name: "wrong csrf token",
			request: func() *http.Request {
				return boardWriteRequest(t, "/api/issues", form, "not-the-token", true)
			},
			status: http.StatusForbidden,
		},
		{
			name: "cross-site fetch metadata",
			request: func() *http.Request {
				request := boardWriteRequest(t, "/api/issues", form, token, true)
				request.Header.Set("Sec-Fetch-Site", "cross-site")
				return request
			},
			status: http.StatusForbidden,
		},
		{
			name: "null origin cross-site",
			request: func() *http.Request {
				request := boardWriteRequest(t, "/api/issues", form, token, true)
				request.Header.Set("Origin", "null")
				request.Header.Set("Sec-Fetch-Site", "cross-site")
				return request
			},
			status: http.StatusForbidden,
		},
		{
			name: "null origin invalid csrf",
			request: func() *http.Request {
				request := boardWriteRequest(t, "/api/issues", form, "bad-token", true)
				request.Header.Set("Origin", "null")
				request.Header.Set("Sec-Fetch-Site", "same-origin")
				return request
			},
			status: http.StatusForbidden,
		},
		{
			name: "cross-origin",
			request: func() *http.Request {
				request := boardWriteRequest(t, "/api/issues", form, token, true)
				request.Header.Set("Origin", "http://evil.example")
				return request
			},
			status: http.StatusForbidden,
		},
		{
			name: "non-form content type",
			request: func() *http.Request {
				request := httptest.NewRequest(http.MethodPost, "/api/issues", strings.NewReader(`{"title":"Task"}`))
				request.Header.Set("Content-Type", "application/json")
				request.Header.Set("X-CSRF-Token", token)
				return request
			},
			status: http.StatusUnsupportedMediaType,
		},
		{
			name: "unknown route",
			request: func() *http.Request {
				return boardWriteRequest(t, "/api/issues/ISSUE-1/delete", form, token, true)
			},
			status: http.StatusNotFound,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, test.request())
			if recorder.Code != test.status {
				t.Fatalf("status = %d, want %d (body %s)", recorder.Code, test.status, recorder.Body.String())
			}
		})
	}
	if len(writes.creates) != 0 {
		t.Fatalf("rejected writes reached the application service: %#v", writes.creates)
	}

	// A same-origin request with the cookie-style form field is accepted.
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, boardWriteRequest(t, "/api/issues", url.Values{
		"csrf_token": {token}, "title": {"Task"},
	}, "", true))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("same-origin form status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	// null origin with Sec-Fetch-Site: same-origin + valid CSRF is accepted
	// (real browser behaviour on loopback addresses like http://127.0.0.1).
	recorder = httptest.NewRecorder()
	request := boardWriteRequest(t, "/api/issues", url.Values{
		"csrf_token": {token}, "title": {"Null-origin task"},
	}, token, true)
	request.Header.Set("Origin", "null")
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("null origin + same-origin status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	// null origin with Sec-Fetch-Site: none + valid CSRF is also accepted.
	recorder = httptest.NewRecorder()
	request = boardWriteRequest(t, "/api/issues", url.Values{
		"csrf_token": {token}, "title": {"Null-origin task 2"},
	}, token, true)
	request.Header.Set("Origin", "null")
	request.Header.Set("Sec-Fetch-Site", "none")
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("null origin + none status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestBoardWriteRedirectsFormsAndMapsConflicts(t *testing.T) {
	conflict := domain.NewError(domain.CodeVersionConflict, "issue version conflict", true)
	writes := &stubBoardWriteService{issue: domain.Issue{ID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", DisplayID: "ISSUE-1"}, updateErr: conflict}
	handler, token := newWritableBoardHandler(t, writes)

	// A browser form post is redirected back to the board (POST/redirect/GET).
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, boardWriteRequest(t, "/api/issues", url.Values{
		"csrf_token": {token}, "title": {"Task"},
	}, token, false))
	if recorder.Code != http.StatusSeeOther || recorder.Header().Get("Location") != "/?notice=created" {
		t.Fatalf("form create = %d %q", recorder.Code, recorder.Header().Get("Location"))
	}

	// An explicit return path keeps the operator on the issue page.
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, boardWriteRequest(t, "/api/issues/ISSUE-1/ready", url.Values{
		"csrf_token": {token}, "expected_version": {"2"}, "return": {"/issues/ISSUE-1"},
	}, token, false))
	if recorder.Code != http.StatusSeeOther || recorder.Header().Get("Location") != "/issues/ISSUE-1?notice=queued" {
		t.Fatalf("queued redirect = %d %q", recorder.Code, recorder.Header().Get("Location"))
	}

	// An off-site return path is ignored, so the form cannot open-redirect.
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, boardWriteRequest(t, "/api/issues", url.Values{
		"csrf_token": {token}, "title": {"Task"}, "return": {"//evil.example/"},
	}, token, false))
	if location := recorder.Header().Get("Location"); location != "/?notice=created" {
		t.Fatalf("open redirect location = %q", location)
	}

	// A version conflict is a clear, non-overwriting failure.
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, boardWriteRequest(t, "/api/issues/ISSUE-1", url.Values{
		"csrf_token": {token}, "expected_version": {"2"}, "title": {"Edited"}, "priority": {"medium"},
	}, token, true))
	if recorder.Code != http.StatusConflict {
		t.Fatalf("conflict status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var payload map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode conflict body: %v", err)
	}
	if payload["code"] != "version_conflict" || strings.TrimSpace(payload["message"]) == "" {
		t.Fatalf("conflict payload = %#v", payload)
	}

	// The same conflict as a form post returns to the board with an error code.
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, boardWriteRequest(t, "/api/issues/ISSUE-1", url.Values{
		"csrf_token": {token}, "expected_version": {"2"}, "title": {"Edited"}, "priority": {"medium"},
	}, token, false))
	if location := recorder.Header().Get("Location"); location != "/?error=version_conflict" {
		t.Fatalf("conflict redirect = %q", location)
	}
}

func TestBoardWriteRejectsMissingFieldsAndKeepsReadOnlyBoardsReadOnly(t *testing.T) {
	readOnly := NewBoardHTTPHandler(&stubBoardService{board: boardResultFixture()})
	recorder := httptest.NewRecorder()
	readOnly.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/issues", strings.NewReader("title=Task")))
	if recorder.Code != http.StatusMethodNotAllowed || recorder.Header().Get("Allow") != "GET, HEAD" {
		t.Fatalf("read-only POST = %d allow %q", recorder.Code, recorder.Header().Get("Allow"))
	}
	recorder = httptest.NewRecorder()
	readOnly.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if strings.Contains(recorder.Body.String(), `name="csrf_token"`) {
		t.Fatal("read-only board rendered write controls")
	}

	writes := &stubBoardWriteService{issue: domain.Issue{ID: "01ARZ3NDEKTSV4RRFFQ69G5FAV"}}
	handler, token := newWritableBoardHandler(t, writes)

	// A missing title is rejected before the application service is called.
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, boardWriteRequest(t, "/api/issues", url.Values{"csrf_token": {token}}, token, true))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("missing title status = %d", recorder.Code)
	}
	var payload map[string]string
	_ = json.Unmarshal(recorder.Body.Bytes(), &payload)
	if payload["code"] != "invalid_input" {
		t.Fatalf("missing title payload = %#v", payload)
	}

	// A missing or malformed expected_version is rejected too.
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, boardWriteRequest(t, "/api/issues/ISSUE-1", url.Values{
		"csrf_token": {token}, "title": {"Edited"}, "priority": {"medium"},
	}, token, true))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("missing expected_version status = %d", recorder.Code)
	}
	if len(writes.updates) != 0 || len(writes.creates) != 0 {
		t.Fatalf("invalid requests reached the application service: %#v %#v", writes.creates, writes.updates)
	}

	// The writable handler advertises POST and keeps GET working.
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPut, "/api/issues", nil))
	if recorder.Code != http.StatusMethodNotAllowed || recorder.Header().Get("Allow") != "GET, HEAD, POST" {
		t.Fatalf("PUT = %d allow %q", recorder.Code, recorder.Header().Get("Allow"))
	}
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "+ New Task") {
		t.Fatalf("writable GET / = %d", recorder.Code)
	}

	// The served page shows the failure banner a redirect asked for.
	_, errorCode := boardPageBanner(&url.URL{RawQuery: "error=version_conflict"})
	message, isError := boardBannerMessage("", errorCode)
	if !isError || !strings.Contains(strings.ToLower(message), "changed this task") {
		t.Fatalf("conflict banner = %q error=%v", message, isError)
	}
	if _, ok := boardErrorMessages["bogus"]; ok {
		t.Fatal("banner code table accepted a code the adapter never emits")
	}
}

// TestBoardWriteControlsRenderOnlyWhereWritesExist keeps the write controls
// honest: the served board shows them with the token, the offline snapshot and
// a read-only board show none, and the issue page prefills the edit form from
// the stored issue with its optimistic version.
func TestBoardWriteControlsRenderOnlyWhereWritesExist(t *testing.T) {
	board := boardResultFixture()
	board.Workflow = domain.NewBoardWorkflowProjection([]domain.BoardWorkflowCard{
		{
			Column: domain.BoardWorkflowColumnReady, IssueID: "issue-1", IssueDisplayID: "ISSUE-1",
			Title: "Ready work", Priority: domain.PriorityHigh, Version: 3,
		},
		{
			Column: domain.BoardWorkflowColumnDone, IssueID: "issue-2", IssueDisplayID: "ISSUE-2",
			Title: "Done work", Priority: domain.PriorityLow, Version: 1,
		},
	}, []domain.BoardWorkflowUnprojected{{
		IssueID: "issue-3", IssueDisplayID: "ISSUE-3", Title: "Open work", StoredStatus: domain.StatusOpen,
		Version: 4, Reason: domain.BoardWorkflowReasonNotReady,
		Detail: domain.BoardWorkflowUnprojectedDetail(domain.BoardWorkflowReasonNotReady),
	}}, domain.BoardWorkflowTruncation{})

	writable, err := renderServedBoardPage(board, boardPageState{CSRFToken: "served-token"})
	if err != nil {
		t.Fatalf("renderServedBoardPage: %v", err)
	}
	for _, want := range []string{
		`action="/api/issues/ISSUE-1/rank"`, `value="3"`, "Move up", "Move down",
		`action="/api/issues/ISSUE-3/ready"`, `value="4"`, "Move to READY",
		`name="csrf_token" value="served-token"`, "New Task",
	} {
		if !strings.Contains(writable, want) {
			t.Fatalf("writable served board is missing %q", want)
		}
	}
	if strings.Contains(writable, `action="/api/issues/ISSUE-2/rank"`) {
		t.Fatal("a non-READY card rendered READY reorder controls")
	}

	readOnlyServed, err := renderServedBoardPage(board, boardPageState{})
	if err != nil {
		t.Fatalf("renderServedBoardPage(read-only): %v", err)
	}
	static, err := renderBoardHTML(board)
	if err != nil {
		t.Fatalf("renderBoardHTML: %v", err)
	}
	for name, html := range map[string]string{"read-only served": readOnlyServed, "static snapshot": static} {
		for _, forbidden := range []string{"csrf_token", "method=\"post\"", "Move to READY"} {
			if strings.Contains(html, forbidden) {
				t.Fatalf("%s rendered write control %q", name, forbidden)
			}
		}
	}

	description := "Stored description"
	criteria := "Stored criteria"
	detail := domain.IssueDetail{Issue: domain.Issue{
		ID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", DisplayID: "ISSUE-9", Title: "Editable",
		Description: &description, AcceptanceCriteria: &criteria,
		Status: domain.StatusOpen, Priority: domain.PriorityHigh, Version: 7,
	}}
	detailHTML, err := renderIssueDetailHTMLWithCSRF(detail, "detail-token", "", "")
	if err != nil {
		t.Fatalf("renderIssueDetailHTMLWithCSRF: %v", err)
	}
	for _, want := range []string{
		`action="/api/issues/ISSUE-9"`, `name="csrf_token" value="detail-token"`,
		`name="expected_version" value="7"`, `value="Editable"`,
		">Stored description<", ">Stored criteria<", `value="high" selected`,
		`action="/api/issues/ISSUE-9/ready"`,
	} {
		if !strings.Contains(detailHTML, want) {
			t.Fatalf("issue page edit form is missing %q:\n%s", want, detailHTML)
		}
	}

	closed := detail
	closed.Issue.Status = domain.StatusDone
	closedHTML, err := renderIssueDetailHTMLWithCSRF(closed, "detail-token", "", "")
	if err != nil {
		t.Fatalf("renderIssueDetailHTMLWithCSRF(done): %v", err)
	}
	if strings.Contains(closedHTML, `action="/api/issues/ISSUE-9/ready"`) {
		t.Fatal("a non-open issue offered the queue action")
	}

	readOnlyDetail, err := renderIssueDetailHTML(detail)
	if err != nil {
		t.Fatalf("renderIssueDetailHTML: %v", err)
	}
	if strings.Contains(readOnlyDetail, "csrf_token") || strings.Contains(readOnlyDetail, "Edit task") {
		t.Fatal("read-only issue page rendered write controls")
	}
}
