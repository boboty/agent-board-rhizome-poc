package cli

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"rhizome-mcp/internal/domain"
)

const boardHTTPContentSecurityPolicy = "default-src 'none'; style-src 'unsafe-inline'; img-src 'self' data:; script-src 'unsafe-inline'; font-src 'self' data:; connect-src 'self'; base-uri 'none'; form-action 'self'"

// BoardHTTPService exposes the board and issue-detail reads used by the served board HTTP adapter.
type BoardHTTPService interface {
	GetBoard(context.Context) (domain.BoardResult, error)
	GetIssueDetail(context.Context, string) (domain.IssueDetail, error)
	Search(context.Context, domain.SearchInput) (domain.SearchPage, error)
}

// NewBoardHTTPHandler serves the board as an interactive loopback-only page and
// JSON API, read-only. POST routes are answered exactly as before this adapter
// grew a write surface, so an embedded read-only board cannot be written to.
func NewBoardHTTPHandler(boardService BoardHTTPService) http.Handler {
	return newBoardHTTPHandler(boardService, newBoardWriteGate(nil))
}

// NewWritableBoardHTTPHandler serves the board plus its minimal human write
// API: create a task, edit a task, queue a task into READY, and reorder the
// READY column. It shares every read route with NewBoardHTTPHandler.
//
// The handler enforces the synchronizer token, same-origin, content-type, body
// size, and method rules itself, but the loopback binding, the Host check, and
// the Origin check on the request path are provided by
// runtime.ServeHTTPServer's wrapper (docs/08). An embedder that serves this
// handler directly must supply equivalent transport protection.
func NewWritableBoardHTTPHandler(boardService BoardHTTPService, writeService BoardWriteService) http.Handler {
	return newBoardHTTPHandler(boardService, newBoardWriteGate(writeService))
}

func newBoardHTTPHandler(boardService BoardHTTPService, gate *boardWriteGate) http.Handler {
	if boardService == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			writeBoardHTTPResponse(w, http.StatusServiceUnavailable, "text/plain; charset=utf-8", []byte("service unavailable"), true)
		})
	}
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		setBoardHTTPHeaders(w)
		if request == nil {
			writeBoardHTTPResponse(w, http.StatusBadRequest, "text/plain; charset=utf-8", []byte("bad request"), true)
			return
		}
		path := "/"
		if request.URL != nil && request.URL.Path != "" {
			path = request.URL.Path
		}
		if request.Method == http.MethodPost {
			if gate.serveBoardWrite(w, request, path) {
				return
			}
			w.Header().Set("Allow", boardAllowedMethods(gate))
			writeBoardHTTPResponse(w, http.StatusMethodNotAllowed, "text/plain; charset=utf-8", []byte(http.StatusText(http.StatusMethodNotAllowed)), true)
			return
		}
		if request.Method != http.MethodGet && request.Method != http.MethodHead {
			w.Header().Set("Allow", boardAllowedMethods(gate))
			writeBoardHTTPResponse(w, http.StatusMethodNotAllowed, "text/plain; charset=utf-8", []byte(http.StatusText(http.StatusMethodNotAllowed)), true)
			return
		}
		switch {
		case path == "/":
			serveBoardPage(w, request, boardService, gate.csrfToken())
		case path == "/api/board":
			serveBoardAPI(w, request.Method, boardService, request.Context(), request.Header.Get("If-None-Match"))
		case path == "/api/search":
			serveSearchAPI(w, request.Method, boardService, request.Context(), request.URL)
		case path == "/search":
			serveSearchPage(w, request.Method, boardService, request.Context(), request.URL)
		case strings.HasPrefix(path, "/issues/"):
			serveIssueDetailPage(w, request.Method, boardService, request.Context(), path, gate.csrfToken(), request.URL)
		case path == "/api/issues" || strings.HasPrefix(path, "/api/issues/"):
			serveIssueDetailAPI(w, request.Method, boardService, request.Context(), request.Header.Get("If-None-Match"), path)
		default:
			writeBoardHTTPResponse(w, http.StatusNotFound, "text/plain; charset=utf-8", []byte(http.StatusText(http.StatusNotFound)), true)
		}
	})
}

// boardAllowedMethods is the Allow header for one handler: a read-only board
// keeps advertising GET and HEAD only.
func boardAllowedMethods(gate *boardWriteGate) string {
	if gate.enabled() {
		return "GET, HEAD, POST"
	}
	return "GET, HEAD"
}

func serveBoardPage(w http.ResponseWriter, request *http.Request, boardService BoardHTTPService, csrfToken string) {
	result, err := boardService.GetBoard(request.Context())
	if err != nil {
		writeBoardHTTPResponse(w, http.StatusInternalServerError, "text/plain; charset=utf-8", []byte(http.StatusText(http.StatusInternalServerError)), true)
		return
	}
	notice, errorCode := boardPageBanner(request.URL)
	body, err := renderServedBoardPage(result, boardPageState{
		Notice: notice, ErrorCode: errorCode, CSRFToken: csrfToken,
	})
	if err != nil {
		writeBoardHTTPResponse(w, http.StatusInternalServerError, "text/plain; charset=utf-8", []byte(http.StatusText(http.StatusInternalServerError)), true)
		return
	}
	if request.Method == http.MethodHead {
		writeBoardHTTPResponse(w, http.StatusOK, "text/html; charset=utf-8", nil, true)
		return
	}
	writeBoardHTTPResponse(w, http.StatusOK, "text/html; charset=utf-8", []byte(body), true)
}

func serveIssueDetailPage(w http.ResponseWriter, method string, boardService BoardHTTPService, ctx context.Context, path string, csrfToken string, requestURL *url.URL) {
	identifier, statusCode, err := parseIssueDetailRoute(path)
	if err != nil {
		writeBoardHTTPResponse(w, statusCode, "text/plain; charset=utf-8", []byte(err.Error()), true)
		return
	}
	result, err := boardService.GetIssueDetail(ctx, identifier)
	if err != nil {
		var domainErr *domain.Error
		if errors.As(err, &domainErr) {
			switch domainErr.Code {
			case domain.CodeIssueNotFound:
				writeBoardHTTPResponse(w, http.StatusNotFound, "text/plain; charset=utf-8", []byte(http.StatusText(http.StatusNotFound)), true)
				return
			case domain.CodeInvalidArgument:
				writeBoardHTTPResponse(w, http.StatusBadRequest, "text/plain; charset=utf-8", []byte(http.StatusText(http.StatusBadRequest)), true)
				return
			}
		}
		writeBoardHTTPResponse(w, http.StatusInternalServerError, "text/plain; charset=utf-8", []byte(http.StatusText(http.StatusInternalServerError)), true)
		return
	}
	notice, errorCode := boardPageBanner(requestURL)
	body, err := renderIssueDetailHTMLWithCSRF(result, csrfToken, notice, errorCode)
	if err != nil {
		writeBoardHTTPResponse(w, http.StatusInternalServerError, "text/plain; charset=utf-8", []byte(http.StatusText(http.StatusInternalServerError)), true)
		return
	}
	if method == http.MethodHead {
		writeBoardHTTPResponse(w, http.StatusOK, "text/html; charset=utf-8", nil, true)
		return
	}
	writeBoardHTTPResponse(w, http.StatusOK, "text/html; charset=utf-8", []byte(body), true)
}

func serveSearchPage(w http.ResponseWriter, method string, boardService BoardHTTPService, ctx context.Context, requestURL *url.URL) {
	result, err := boardService.GetBoard(ctx)
	if err != nil {
		writeBoardHTTPResponse(w, http.StatusInternalServerError, "text/plain; charset=utf-8", []byte(http.StatusText(http.StatusInternalServerError)), true)
		return
	}
	state := servedBoardSearchState{}
	input, err := parseBoardSearchRequest(requestURL)
	if err != nil {
		state.Invalid = true
		state.StatusMessage = buildBoardSearchStatusMessage(state.Query, 0, false, true, false)
		body, err := renderServedBoardHTMLWithSearchState(result, state)
		if err != nil {
			writeBoardHTTPResponse(w, http.StatusInternalServerError, "text/plain; charset=utf-8", []byte(http.StatusText(http.StatusInternalServerError)), true)
			return
		}
		if method == http.MethodHead {
			writeBoardHTTPResponse(w, http.StatusOK, "text/html; charset=utf-8", nil, true)
			return
		}
		writeBoardHTTPResponse(w, http.StatusOK, "text/html; charset=utf-8", []byte(body), true)
		return
	}
	state.Query = strings.TrimSpace(input.Query)
	if len(input.EntityTypes) > 0 {
		state.EntityType = string(input.EntityTypes[0])
	}
	if state.Query == "" {
		state.StatusMessage = buildBoardSearchStatusMessage(state.Query, 0, false, false, false)
		body, err := renderServedBoardHTMLWithSearchState(result, state)
		if err != nil {
			writeBoardHTTPResponse(w, http.StatusInternalServerError, "text/plain; charset=utf-8", []byte(http.StatusText(http.StatusInternalServerError)), true)
			return
		}
		if method == http.MethodHead {
			writeBoardHTTPResponse(w, http.StatusOK, "text/html; charset=utf-8", nil, true)
			return
		}
		writeBoardHTTPResponse(w, http.StatusOK, "text/html; charset=utf-8", []byte(body), true)
		return
	}
	page, err := boardService.Search(ctx, input)
	if err != nil {
		var domainErr *domain.Error
		if errors.As(err, &domainErr) {
			if domainErr.Code == domain.CodeInvalidArgument {
				state.Invalid = true
				state.StatusMessage = buildBoardSearchStatusMessage(state.Query, 0, false, true, false)
			} else {
				state.Error = true
				state.StatusMessage = buildBoardSearchStatusMessage(state.Query, 0, false, false, true)
			}
		} else {
			state.Error = true
			state.StatusMessage = buildBoardSearchStatusMessage(state.Query, 0, false, false, true)
		}
		body, err := renderServedBoardHTMLWithSearchState(result, state)
		if err != nil {
			writeBoardHTTPResponse(w, http.StatusInternalServerError, "text/plain; charset=utf-8", []byte(http.StatusText(http.StatusInternalServerError)), true)
			return
		}
		if method == http.MethodHead {
			writeBoardHTTPResponse(w, http.StatusOK, "text/html; charset=utf-8", nil, true)
			return
		}
		writeBoardHTTPResponse(w, http.StatusOK, "text/html; charset=utf-8", []byte(body), true)
		return
	}
	state.Results = page.Results
	state.HasMore = page.HasMore
	state.StatusMessage = buildBoardSearchStatusMessage(state.Query, len(state.Results), state.HasMore, false, false)
	body, err := renderServedBoardHTMLWithSearchState(result, state)
	if err != nil {
		writeBoardHTTPResponse(w, http.StatusInternalServerError, "text/plain; charset=utf-8", []byte(http.StatusText(http.StatusInternalServerError)), true)
		return
	}
	if method == http.MethodHead {
		writeBoardHTTPResponse(w, http.StatusOK, "text/html; charset=utf-8", nil, true)
		return
	}
	writeBoardHTTPResponse(w, http.StatusOK, "text/html; charset=utf-8", []byte(body), true)
}

func serveSearchAPI(w http.ResponseWriter, method string, boardService BoardHTTPService, ctx context.Context, requestURL *url.URL) {
	input, err := parseBoardSearchRequest(requestURL)
	if err != nil {
		writeBoardHTTPResponse(w, http.StatusBadRequest, "application/json; charset=utf-8", []byte(`{"error":"invalid search request"}`), true)
		return
	}
	if strings.TrimSpace(input.Query) == "" {
		writeBoardHTTPResponse(w, http.StatusBadRequest, "application/json; charset=utf-8", []byte(`{"error":"invalid search request"}`), true)
		return
	}
	page, err := boardService.Search(ctx, input)
	if err != nil {
		var domainErr *domain.Error
		if errors.As(err, &domainErr) {
			if domainErr.Code == domain.CodeInvalidArgument {
				writeBoardHTTPResponse(w, http.StatusBadRequest, "application/json; charset=utf-8", []byte(`{"error":"invalid search request"}`), true)
				return
			}
		}
		writeBoardHTTPResponse(w, http.StatusInternalServerError, "application/json; charset=utf-8", []byte(`{"error":"internal server error"}`), true)
		return
	}
	payload, err := json.Marshal(boardSearchResponseFromDomain(page))
	if err != nil {
		writeBoardHTTPResponse(w, http.StatusInternalServerError, "application/json; charset=utf-8", []byte(`{"error":"internal server error"}`), true)
		return
	}
	if method == http.MethodHead {
		writeBoardHTTPResponse(w, http.StatusOK, "application/json; charset=utf-8", nil, false)
		return
	}
	writeBoardHTTPResponse(w, http.StatusOK, "application/json; charset=utf-8", payload, true)
}

func serveIssueDetailAPI(w http.ResponseWriter, method string, boardService BoardHTTPService, ctx context.Context, ifNoneMatch string, path string) {
	identifier, statusCode, err := parseIssueDetailRoute(strings.TrimPrefix(path, "/api"))
	if err != nil {
		writeBoardHTTPResponse(w, statusCode, "text/plain; charset=utf-8", []byte(err.Error()), true)
		return
	}
	result, err := boardService.GetIssueDetail(ctx, identifier)
	if err != nil {
		var domainErr *domain.Error
		if errors.As(err, &domainErr) {
			switch domainErr.Code {
			case domain.CodeIssueNotFound:
				writeBoardHTTPResponse(w, http.StatusNotFound, "text/plain; charset=utf-8", []byte(http.StatusText(http.StatusNotFound)), true)
				return
			case domain.CodeInvalidArgument:
				writeBoardHTTPResponse(w, http.StatusBadRequest, "text/plain; charset=utf-8", []byte(http.StatusText(http.StatusBadRequest)), true)
				return
			}
		}
		writeBoardHTTPResponse(w, http.StatusInternalServerError, "text/plain; charset=utf-8", []byte(http.StatusText(http.StatusInternalServerError)), true)
		return
	}
	payload, err := json.Marshal(result)
	if err != nil {
		writeBoardHTTPResponse(w, http.StatusInternalServerError, "application/json; charset=utf-8", []byte(`{"error":"internal server error"}`), true)
		return
	}
	etag := semanticETag(payload)
	if etagMatches(ifNoneMatch, etag) {
		w.Header().Set("ETag", etag)
		writeBoardHTTPResponse(w, http.StatusNotModified, "application/json; charset=utf-8", nil, false)
		return
	}
	w.Header().Set("ETag", etag)
	if method == http.MethodHead {
		writeBoardHTTPResponse(w, http.StatusOK, "application/json; charset=utf-8", nil, false)
		return
	}
	writeBoardHTTPResponse(w, http.StatusOK, "application/json; charset=utf-8", payload, true)
}

func parseIssueDetailRoute(path string) (string, int, error) {
	if !strings.HasPrefix(path, "/issues/") {
		return "", http.StatusNotFound, errors.New(http.StatusText(http.StatusNotFound))
	}
	rest := strings.TrimPrefix(path, "/issues/")
	if rest == "" || strings.Contains(rest, "/") {
		return "", http.StatusBadRequest, errors.New(http.StatusText(http.StatusBadRequest))
	}
	identifier, err := url.PathUnescape(rest)
	if err != nil || identifier == "" || strings.Contains(identifier, "/") {
		return "", http.StatusBadRequest, errors.New(http.StatusText(http.StatusBadRequest))
	}
	if strings.Contains(identifier, "%2F") || strings.Contains(identifier, "%5C") {
		return "", http.StatusBadRequest, errors.New(http.StatusText(http.StatusBadRequest))
	}
	return identifier, http.StatusOK, nil
}

func parseBoardSearchRequest(requestURL *url.URL) (domain.SearchInput, error) {
	input := domain.SearchInput{}
	if requestURL == nil {
		return input, nil
	}
	values := requestURL.Query()
	if query := strings.TrimSpace(values.Get("q")); query != "" {
		input.Query = query
	}
	for _, raw := range values["entity_type"] {
		entityType := domain.SearchEntityType(strings.TrimSpace(raw))
		if entityType == "" {
			continue
		}
		input.EntityTypes = append(input.EntityTypes, entityType)
	}
	if raw := strings.TrimSpace(values.Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 || parsed > 100 {
			return domain.SearchInput{}, fmt.Errorf("invalid limit")
		}
		input.Limit = parsed
	}
	input.Cursor = strings.TrimSpace(values.Get("cursor"))
	if raw := strings.TrimSpace(values.Get("snippet_length")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 || parsed > 1000 {
			return domain.SearchInput{}, fmt.Errorf("invalid snippet_length")
		}
		input.SnippetLength = parsed
	}
	if raw := strings.TrimSpace(values.Get("include_archived")); raw != "" {
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			return domain.SearchInput{}, fmt.Errorf("invalid include_archived")
		}
		input.IncludeArchived = parsed
	}
	for key := range values {
		switch key {
		case "q", "entity_type", "limit", "cursor", "snippet_length", "include_archived":
			continue
		default:
			return domain.SearchInput{}, fmt.Errorf("unsupported parameter")
		}
	}
	return input, nil
}

func boardSearchResponseFromDomain(page domain.SearchPage) map[string]any {
	results := make([]map[string]any, len(page.Results))
	for index, item := range page.Results {
		result := map[string]any{
			"entity_type": string(item.EntityType),
			"entity_id":   item.EntityID,
			"title":       item.Title,
			"snippet":     item.Snippet,
			"score":       item.Score,
		}
		if item.IssueID != nil {
			result["issue_id"] = *item.IssueID
		}
		results[index] = result
	}
	response := map[string]any{"results": results}
	if page.NextCursor != nil {
		response["next_cursor"] = *page.NextCursor
	} else {
		response["next_cursor"] = nil
	}
	response["has_more"] = page.HasMore
	return response
}

func serveBoardAPI(w http.ResponseWriter, method string, boardService BoardHTTPService, ctx context.Context, ifNoneMatch string) {
	result, err := boardService.GetBoard(ctx)
	if err != nil {
		writeBoardHTTPResponse(w, http.StatusInternalServerError, "application/json; charset=utf-8", []byte(`{"error":"internal server error"}`), true)
		return
	}
	payload, err := json.MarshalIndent(boardResponseFromDomain(result), "", "  ")
	if err != nil {
		writeBoardHTTPResponse(w, http.StatusInternalServerError, "application/json; charset=utf-8", []byte(`{"error":"internal server error"}`), true)
		return
	}
	etag := semanticBoardETag(result)
	if etagMatches(ifNoneMatch, etag) {
		w.Header().Set("ETag", etag)
		writeBoardHTTPResponse(w, http.StatusNotModified, "application/json; charset=utf-8", nil, false)
		return
	}
	w.Header().Set("ETag", etag)
	if method == http.MethodHead {
		writeBoardHTTPResponse(w, http.StatusOK, "application/json; charset=utf-8", nil, false)
		return
	}
	writeBoardHTTPResponse(w, http.StatusOK, "application/json; charset=utf-8", payload, true)
}

func semanticBoardETag(result domain.BoardResult) string {
	payload := boardETagPayload{StatusCounts: make([]boardETagStatusCount, len(result.StatusCounts)), ActiveAttempts: make([]boardETagActiveAttempt, len(result.ActiveAttempts)), AttemptGates: boardAttemptGatesFromDomain(result.AttemptGates), ActiveReservations: make([]boardETagReservation, len(result.ActiveReservations)), BlockedIssues: make([]IssueSummary, len(result.BlockedIssues)), ReviewRequests: make([]boardETagReviewRequest, len(result.ReviewRequests)), PlanningGraph: boardETagGraph{Nodes: make([]IssueSummary, len(result.PlanningGraph.Nodes)), Edges: result.PlanningGraph.Edges, EntryPoints: result.PlanningGraph.EntryPoints, BlockingNodes: result.PlanningGraph.BlockingNodes, Summary: result.PlanningGraph.Summary, Truncated: result.PlanningGraph.Truncated, RetainedNodeCount: len(result.PlanningGraph.Nodes)}, Truncation: BoardTruncation{BlockedIssues: result.Truncation.BlockedIssues, ActiveAttempts: result.Truncation.ActiveAttempts, ActiveReservations: result.Truncation.ActiveReservations, ReviewRequests: result.Truncation.ReviewRequests}, Workflow: boardWorkflowFromDomain(result.Workflow)}
	for index, item := range result.StatusCounts {
		payload.StatusCounts[index] = boardETagStatusCount{EffectiveStatus: string(item.EffectiveStatus), Count: item.Count}
	}
	for index, item := range result.ActiveAttempts {
		payload.ActiveAttempts[index] = boardETagActiveAttempt{AttemptID: item.AttemptID, IssueID: item.IssueID, IssueDisplayID: item.IssueDisplayID, IssueTitle: item.IssueTitle, Kind: string(item.Kind), SessionID: copyOptionalString(item.SessionID), SessionLabel: copyOptionalString(item.SessionLabel), SessionInstanceKey: copyOptionalString(item.SessionInstanceKey), SessionClientName: copyOptionalString(item.SessionClientName), SessionModel: copyOptionalString(item.SessionModel), SessionWorktree: copyOptionalString(item.SessionWorktree), StartedAt: item.StartedAt.UTC(), LeaseExpiresAt: item.LeaseExpiresAt.UTC()}
	}
	for index, item := range result.ActiveReservations {
		payload.ActiveReservations[index] = boardETagReservation{ID: item.ID, IssueID: item.IssueID, AttemptID: item.AttemptID, Kind: string(item.Kind), DisplayValue: item.DisplayValue, Status: string(item.Status), Version: item.Version}
	}
	for index, item := range result.BlockedIssues {
		payload.BlockedIssues[index] = issueFromDomainProjection(item)
	}
	for index, item := range result.ReviewRequests {
		payload.ReviewRequests[index] = boardETagReviewRequest{ID: item.ID, IssueID: item.IssueID, Status: string(item.Status), TargetIssueVersion: item.TargetIssueVersion, CreatedAt: item.CreatedAt.UTC()}
	}
	for index, item := range result.PlanningGraph.Nodes {
		payload.PlanningGraph.Nodes[index] = issueFromDomainProjection(item)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return semanticETag(nil)
	}
	return semanticETag(body)
}

func semanticETag(payload []byte) string {
	if len(payload) == 0 {
		return fmt.Sprintf("\"%x\"", sha256.Sum256(nil))
	}
	sum := sha256.Sum256(payload)
	return fmt.Sprintf("\"%x\"", sum)
}

func etagMatches(candidate string, actual string) bool {
	if candidate == "" || actual == "" {
		return false
	}
	candidate = strings.TrimSpace(candidate)
	if candidate == "*" {
		return true
	}
	actual = strings.TrimSpace(actual)
	if strings.HasPrefix(candidate, "W/\"") || strings.HasPrefix(candidate, "w/\"") {
		candidate = strings.TrimPrefix(strings.TrimPrefix(candidate, "W/"), "w/")
	}
	candidate = strings.Trim(candidate, `"`)
	actual = strings.Trim(actual, `"`)
	return candidate == actual
}

type boardETagPayload struct {
	StatusCounts       []boardETagStatusCount   `json:"status_counts"`
	ActiveAttempts     []boardETagActiveAttempt `json:"active_attempts"`
	AttemptGates       []BoardAttemptGates      `json:"attempt_gates"`
	ActiveReservations []boardETagReservation   `json:"active_reservations"`
	BlockedIssues      []IssueSummary           `json:"blocked_issues"`
	ReviewRequests     []boardETagReviewRequest `json:"review_requests"`
	PlanningGraph      boardETagGraph           `json:"planning_graph"`
	Truncation         BoardTruncation          `json:"truncation"`
	// Workflow participates in the semantic ETag so a card moving between
	// columns reaches a polling client even when nothing else changed.
	Workflow BoardWorkflow `json:"workflow"`
}

type boardETagReservation struct {
	ID           string `json:"id"`
	IssueID      string `json:"issue_id"`
	AttemptID    string `json:"attempt_id"`
	Kind         string `json:"kind"`
	DisplayValue string `json:"display_value"`
	Status       string `json:"status"`
	Version      int64  `json:"version"`
}

type boardETagStatusCount struct {
	EffectiveStatus string `json:"effective_status"`
	Count           int64  `json:"count"`
}

type boardETagActiveAttempt struct {
	AttemptID          string    `json:"attempt_id"`
	IssueID            string    `json:"issue_id"`
	IssueDisplayID     string    `json:"issue_display_id"`
	IssueTitle         string    `json:"issue_title"`
	Kind               string    `json:"kind"`
	SessionID          *string   `json:"session_id,omitempty"`
	SessionLabel       *string   `json:"session_label,omitempty"`
	SessionInstanceKey *string   `json:"session_instance_key,omitempty"`
	SessionClientName  *string   `json:"session_client_name,omitempty"`
	SessionModel       *string   `json:"session_model,omitempty"`
	SessionWorktree    *string   `json:"session_worktree,omitempty"`
	StartedAt          time.Time `json:"started_at"`
	LeaseExpiresAt     time.Time `json:"lease_expires_at"`
}

type boardETagReviewRequest struct {
	ID                 string    `json:"id"`
	IssueID            string    `json:"issue_id"`
	Status             string    `json:"status"`
	TargetIssueVersion int64     `json:"target_issue_version"`
	CreatedAt          time.Time `json:"created_at"`
}

type boardETagGraph struct {
	Nodes             []IssueSummary      `json:"nodes"`
	Edges             []domain.GraphEdge  `json:"edges"`
	EntryPoints       []string            `json:"entry_points"`
	BlockingNodes     []string            `json:"blocking_nodes"`
	Summary           domain.GraphSummary `json:"summary"`
	Truncated         bool                `json:"truncated"`
	RetainedNodeCount int                 `json:"retained_node_count"`
}

func setBoardHTTPHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Security-Policy", boardHTTPContentSecurityPolicy)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "no-store")
}

func writeBoardHTTPResponse(w http.ResponseWriter, statusCode int, contentType string, body []byte, includeBody bool) {
	if contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	if len(body) > 0 {
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	}
	w.WriteHeader(statusCode)
	if includeBody && len(body) > 0 {
		_, _ = w.Write(body)
	}
}
