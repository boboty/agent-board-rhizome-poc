package cli

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"rhizome-mcp/internal/application"
	"rhizome-mcp/internal/domain"
)

// boardNoticeMessages and boardErrorMessages are the only banner codes the
// board redirects with, and the only ones it will render. Keeping them as a
// closed set means a crafted query string cannot inject arbitrary text into
// the page.
var boardNoticeMessages = map[string]string{
	"created":   "Task created.",
	"updated":   "Task updated.",
	"queued":    "Task queued into READY.",
	"reordered": "READY order updated.",
}

var boardErrorMessages = map[string]string{
	"version_conflict": "Someone else changed this task first, so your change was not applied. The latest data is shown; review it and try again.",
	"conflict":         "The task's current state does not allow this change (it may be archived or held by an active attempt). The latest data is shown.",
	"invalid_input":    "The submitted values were not accepted. The latest data is shown.",
	"not_found":        "That task no longer exists. The latest data is shown.",
	"failed":           "The change could not be completed. The latest data is shown.",
}

// boardWriteBodyLimit bounds one write request body. The board's forms are a
// handful of short fields, so the cap is deliberately small.
const boardWriteBodyLimit = 64 << 10

// boardCSRFHeader is accepted in place of the form field so a fetch-based
// client does not have to embed the token in a body it may build in JSON.
const boardCSRFHeader = "X-CSRF-Token"

// BoardWriteService is the narrow application surface the served board writes
// through. Every method maps to one existing application use case; the adapter
// validates HTTP-level concerns (method, origin, CSRF, field presence) and the
// application layer owns the domain rules, so no rule is duplicated here.
type BoardWriteService interface {
	CreateTask(context.Context, application.CreateBoardTaskInput) (application.CreateBoardTaskResult, error)
	UpdateTask(context.Context, application.UpdateBoardTaskInput) (application.UpdateBoardTaskResult, error)
	MoveTaskToReady(context.Context, application.MoveBoardTaskToReadyInput) (application.UpdateBoardTaskResult, error)
	MoveReadyTask(context.Context, application.MoveBoardReadyTaskInput) (application.MoveBoardReadyTaskResult, error)
}

// boardWriteGate owns the served board's write authentication state: a
// per-process synchronizer token that every served form embeds and every write
// must echo. It also holds the optional write service, so a board started
// without one keeps today's read-only behavior exactly.
type boardWriteGate struct {
	service BoardWriteService
	// token is the anti-CSRF synchronizer token. It is never written to a
	// cookie, because a cross-site page cannot read it out of the same-origin
	// HTML that carries it; the token requirement is what makes a cross-site
	// form post fail regardless of the Origin header.
	token string
	// ready is false when the process could not generate a token. In that case
	// every write is refused rather than served without CSRF protection.
	ready bool
}

func newBoardWriteGate(service BoardWriteService) *boardWriteGate {
	if service == nil {
		return &boardWriteGate{}
	}
	token, err := newBoardCSRFToken()
	if err != nil {
		return &boardWriteGate{service: service}
	}
	return &boardWriteGate{service: service, token: token, ready: true}
}

// enabled reports whether this handler accepts writes at all.
func (gate *boardWriteGate) enabled() bool { return gate != nil && gate.service != nil }

// csrfToken returns the token served forms must embed. It is empty when writes
// are disabled, which is how the templates decide not to render write controls.
func (gate *boardWriteGate) csrfToken() string {
	if gate == nil || !gate.ready {
		return ""
	}
	return gate.token
}

func newBoardCSRFToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// serveBoardWrite owns every POST route. It returns true when it handled the
// request, so the caller can fall through to its 405 for anything else.
func (gate *boardWriteGate) serveBoardWrite(w http.ResponseWriter, request *http.Request, path string) bool {
	if !gate.enabled() {
		return false
	}
	if request.Method != http.MethodPost {
		return false
	}
	if !gate.ready {
		writeBoardHTTPResponse(w, http.StatusServiceUnavailable, "text/plain; charset=utf-8",
			[]byte("board writes are unavailable"), true)
		return true
	}
	if !boardWriteSameOrigin(request) {
		writeBoardHTTPResponse(w, http.StatusForbidden, "text/plain; charset=utf-8",
			[]byte(http.StatusText(http.StatusForbidden)), true)
		return true
	}
	contentType := request.Header.Get("Content-Type")
	if !boardFormContentType(contentType) {
		writeBoardHTTPResponse(w, http.StatusUnsupportedMediaType, "text/plain; charset=utf-8",
			[]byte("application/x-www-form-urlencoded required"), true)
		return true
	}
	request.Body = http.MaxBytesReader(w, request.Body, boardWriteBodyLimit)
	if err := request.ParseForm(); err != nil {
		gate.writeBoardFailure(w, request, domain.NewError(domain.CodeInvalidArgument, "request body is invalid", false))
		return true
	}
	if !gate.csrfMatches(request) {
		writeBoardHTTPResponse(w, http.StatusForbidden, "text/plain; charset=utf-8",
			[]byte(http.StatusText(http.StatusForbidden)), true)
		return true
	}
	switch {
	case path == "/api/issues":
		gate.createBoardTask(w, request)
	case strings.HasPrefix(path, "/api/issues/"):
		gate.mutateBoardTask(w, request, strings.TrimPrefix(path, "/api/issues/"))
	default:
		writeBoardHTTPResponse(w, http.StatusNotFound, "text/plain; charset=utf-8",
			[]byte(http.StatusText(http.StatusNotFound)), true)
	}
	return true
}

// boardWriteSameOrigin is the HTTP-layer half of CSRF defense: a write is only
// accepted when the browser-visible origin agrees with the Host it was sent to,
// and when the browser did not label the request cross-site. The synchronizer
// token is the other half and is required unconditionally.
func boardWriteSameOrigin(request *http.Request) bool {
	site := strings.ToLower(strings.TrimSpace(request.Header.Get("Sec-Fetch-Site")))
	origin := strings.TrimSpace(request.Header.Get("Origin"))

	// Reject requests the browser explicitly labelled cross-site.
	if site == "cross-site" {
		return false
	}

	if origin == "" {
		// No Origin header — plain HTML form post or a trusted embedder.
		// Allow through; the CSRF token check is the authoritative defense.
		return true
	}

	// Browsers serialize opaque security origins (e.g. loopback pages at
	// http://127.0.0.1) as the literal string "null".  Only accept it
	// when the request carries explicit same-site metadata so that a
	// cross-site script cannot trivially forge a null Origin.
	if origin == "null" {
		return site == "same-origin" || site == "none"
	}

	// Normal origin — must match the Host we received the request on.
	expected := "http://" + strings.TrimSpace(request.Host)
	if request.TLS != nil {
		expected = "https://" + strings.TrimSpace(request.Host)
	}
	return origin == expected
}

func boardFormContentType(value string) bool {
	mediaType := strings.TrimSpace(strings.Split(value, ";")[0])
	return strings.EqualFold(mediaType, "application/x-www-form-urlencoded")
}

func (gate *boardWriteGate) csrfMatches(request *http.Request) bool {
	candidate := request.PostFormValue("csrf_token")
	if candidate == "" {
		candidate = request.Header.Get(boardCSRFHeader)
	}
	if candidate == "" || gate.token == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(candidate), []byte(gate.token)) == 1
}

func (gate *boardWriteGate) createBoardTask(w http.ResponseWriter, request *http.Request) {
	title := strings.TrimSpace(request.PostFormValue("title"))
	if title == "" {
		gate.writeBoardFailure(w, request, boardRequiredField("title"))
		return
	}
	status := domain.Status(strings.TrimSpace(request.PostFormValue("status")))
	priority := domain.Priority(strings.TrimSpace(request.PostFormValue("priority")))
	var readyRank *int64
	if raw := strings.TrimSpace(request.PostFormValue("ready_rank")); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			gate.writeBoardFailure(w, request, boardInvalidField("ready_rank"))
			return
		}
		readyRank = &parsed
	}
	result, err := gate.service.CreateTask(request.Context(), application.CreateBoardTaskInput{
		Fields: application.BoardTaskFields{
			Title:              title,
			Description:        boardFormText(request, "description"),
			AcceptanceCriteria: boardFormText(request, "acceptance_criteria"),
			Priority:           priority,
		},
		Status:    status,
		ReadyRank: readyRank,
	})
	if err != nil {
		gate.writeBoardFailure(w, request, err)
		return
	}
	gate.writeBoardSuccess(w, request, http.StatusCreated, "created", result.Issue)
}

func (gate *boardWriteGate) mutateBoardTask(w http.ResponseWriter, request *http.Request, remainder string) {
	segments := strings.Split(strings.Trim(remainder, "/"), "/")
	identifier := segments[0]
	action := ""
	if len(segments) == 2 {
		action = segments[1]
	}
	// Reject an unknown route before reading its fields, so a wrong URL is a
	// 404 rather than a misleading validation error.
	if identifier == "" || len(segments) > 2 ||
		(action != "" && action != "ready" && action != "rank") {
		writeBoardHTTPResponse(w, http.StatusNotFound, "text/plain; charset=utf-8",
			[]byte(http.StatusText(http.StatusNotFound)), true)
		return
	}
	expectedVersion, err := boardExpectedVersion(request)
	if err != nil {
		gate.writeBoardFailure(w, request, err)
		return
	}
	switch action {
	case "":
		title := strings.TrimSpace(request.PostFormValue("title"))
		if title == "" {
			gate.writeBoardFailure(w, request, boardRequiredField("title"))
			return
		}
		result, err := gate.service.UpdateTask(request.Context(), application.UpdateBoardTaskInput{
			IssueID:         identifier,
			ExpectedVersion: expectedVersion,
			Fields: application.BoardTaskFields{
				Title:              title,
				Description:        boardFormText(request, "description"),
				AcceptanceCriteria: boardFormText(request, "acceptance_criteria"),
				Priority:           domain.Priority(strings.TrimSpace(request.PostFormValue("priority"))),
			},
		})
		if err != nil {
			gate.writeBoardFailure(w, request, err)
			return
		}
		gate.writeBoardSuccess(w, request, http.StatusOK, "updated", result.Issue)
	case "ready":
		var readyRank *int64
		if raw := strings.TrimSpace(request.PostFormValue("ready_rank")); raw != "" {
			parsed, err := strconv.ParseInt(raw, 10, 64)
			if err != nil {
				gate.writeBoardFailure(w, request, boardInvalidField("ready_rank"))
				return
			}
			readyRank = &parsed
		}
		result, err := gate.service.MoveTaskToReady(request.Context(), application.MoveBoardTaskToReadyInput{
			IssueID: identifier, ExpectedVersion: expectedVersion, ReadyRank: readyRank,
		})
		if err != nil {
			gate.writeBoardFailure(w, request, err)
			return
		}
		gate.writeBoardSuccess(w, request, http.StatusOK, "queued", result.Issue)
	case "rank":
		result, err := gate.service.MoveReadyTask(request.Context(), application.MoveBoardReadyTaskInput{
			IssueID:         identifier,
			ExpectedVersion: expectedVersion,
			Direction:       application.BoardReadyMoveDirection(strings.TrimSpace(request.PostFormValue("direction"))),
		})
		if err != nil {
			gate.writeBoardFailure(w, request, err)
			return
		}
		gate.writeBoardSuccess(w, request, http.StatusOK, "reordered", result.Issue)
	}
}

// writeBoardSuccess finishes a write. A JSON client (Accept: application/json)
// gets the persisted issue; a browser form post is redirected back to the board
// with a notice code so the page reloads fresh state (POST/redirect/GET).
func (gate *boardWriteGate) writeBoardSuccess(w http.ResponseWriter, request *http.Request, status int, notice string, issue domain.Issue) {
	if boardWantsJSON(request) {
		body, err := json.Marshal(boardWriteIssuePayload(issue))
		if err != nil {
			writeBoardHTTPResponse(w, http.StatusInternalServerError, "text/plain; charset=utf-8",
				[]byte(http.StatusText(http.StatusInternalServerError)), true)
			return
		}
		writeBoardHTTPResponse(w, status, "application/json; charset=utf-8", body, true)
		return
	}
	w.Header().Set("Location", boardReturnPath(request)+"?notice="+notice)
	w.WriteHeader(http.StatusSeeOther)
}

// writeBoardFailure maps one domain error onto an HTTP response. A JSON client
// gets a status and a stable code; a form post is redirected to the board with
// an error code, which reloads current data instead of showing a dead end.
func (gate *boardWriteGate) writeBoardFailure(w http.ResponseWriter, request *http.Request, err error) {
	status, code := boardWriteErrorStatus(err)
	if boardWantsJSON(request) {
		body, marshalErr := json.Marshal(map[string]string{"code": code, "message": boardWriteErrorMessage(err)})
		if marshalErr != nil {
			writeBoardHTTPResponse(w, http.StatusInternalServerError, "text/plain; charset=utf-8",
				[]byte(http.StatusText(http.StatusInternalServerError)), true)
			return
		}
		writeBoardHTTPResponse(w, status, "application/json; charset=utf-8", body, true)
		return
	}
	w.Header().Set("Location", boardReturnPath(request)+"?error="+code)
	w.WriteHeader(http.StatusSeeOther)
}

// boardReturnPath is where a form post sends the browser next. Only the board
// itself and one issue page are accepted, so a crafted form value cannot turn
// the redirect into an open redirect.
func boardReturnPath(request *http.Request) string {
	value := strings.TrimSpace(request.PostFormValue("return"))
	if value == "/" {
		return "/"
	}
	if strings.HasPrefix(value, "/issues/") && !strings.ContainsAny(value, "\\?#\r\n") {
		return value
	}
	return "/"
}

func boardWantsJSON(request *http.Request) bool {
	accept := strings.ToLower(request.Header.Get("Accept"))
	return strings.Contains(accept, "application/json")
}

func boardWriteErrorStatus(err error) (int, string) {
	var domainErr *domain.Error
	if errors.As(err, &domainErr) {
		switch domainErr.Code {
		case domain.CodeVersionConflict:
			return http.StatusConflict, "version_conflict"
		case domain.CodeIssueNotFound:
			return http.StatusNotFound, "not_found"
		case domain.CodeInvalidArgument, domain.CodeValidationError:
			return http.StatusBadRequest, "invalid_input"
		case domain.CodeIssueArchived, domain.CodeActiveAttemptExists:
			// The request is well formed; the issue's state refuses it. A
			// redirect that says "failed" would hide a usable explanation.
			return http.StatusConflict, "conflict"
		}
	}
	return http.StatusInternalServerError, "failed"
}

func boardWriteErrorMessage(err error) string {
	var domainErr *domain.Error
	if errors.As(err, &domainErr) && strings.TrimSpace(domainErr.Message) != "" {
		return domainErr.Message
	}
	return "the write could not be completed"
}

// boardWriteIssuePayload is the JSON shape a JSON write client receives. It is
// deliberately the same stable projection the CLI uses for one issue, not a
// generic issue envelope.
type boardWriteIssuePayloadResponse struct {
	ID                 string  `json:"id"`
	DisplayID          string  `json:"display_id"`
	Type               string  `json:"type"`
	Title              string  `json:"title"`
	Status             string  `json:"status"`
	Priority           string  `json:"priority"`
	ReadyRank          *int64  `json:"ready_rank"`
	Version            int64   `json:"version"`
	Description        *string `json:"description"`
	AcceptanceCriteria *string `json:"acceptance_criteria"`
}

func boardWriteIssuePayload(issue domain.Issue) boardWriteIssuePayloadResponse {
	return boardWriteIssuePayloadResponse{
		ID: issue.ID, DisplayID: issue.DisplayID, Type: string(issue.Type), Title: issue.Title,
		Status: string(issue.Status), Priority: string(issue.Priority), ReadyRank: copyOptionalInt64(issue.ReadyRank),
		Version: issue.Version, Description: copyOptionalString(issue.Description),
		AcceptanceCriteria: copyOptionalString(issue.AcceptanceCriteria),
	}
}

func boardExpectedVersion(request *http.Request) (int64, error) {
	raw := strings.TrimSpace(request.PostFormValue("expected_version"))
	if raw == "" {
		return 0, boardRequiredField("expected_version")
	}
	parsed, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || parsed < 1 {
		return 0, boardInvalidField("expected_version")
	}
	return parsed, nil
}

// boardFormText maps one optional textarea/input onto the patch field's absent
// semantics: a blank submission clears the stored text, because an empty
// textarea is how a person expresses "no description".
func boardFormText(request *http.Request, field string) *string {
	if !request.PostForm.Has(field) {
		return nil
	}
	value := strings.TrimSpace(request.PostFormValue(field))
	if value == "" {
		return nil
	}
	return &value
}

func boardRequiredField(field string) error {
	return domain.NewError(domain.CodeInvalidArgument, fmt.Sprintf("%s is required", field), false,
		domain.Detail{Field: field, Code: "REQUIRED"})
}

func boardInvalidField(field string) error {
	return domain.NewError(domain.CodeInvalidArgument, fmt.Sprintf("%s is invalid", field), false,
		domain.Detail{Field: field, Code: "INVALID"})
}
