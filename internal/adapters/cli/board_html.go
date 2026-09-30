package cli

import (
	"bytes"
	"fmt"
	"net/url"
	"strings"
	"time"

	"rhizome-mcp/internal/domain"
)

// renderBoardHTML renders a fully self-contained HTML status board: no
// <script src=...>, no <link rel="stylesheet" href=...>, no CDN or network
// references of any kind. The dependency/planning graph is rendered as
// hand-built inline SVG (see renderBoardGraphSVG), and the same graph is also
// included as portable Mermaid source text for copying into any renderer.
func renderBoardHTML(result domain.BoardResult) (string, error) {
	vm := newBoardStaticPageViewModel(result)
	return renderBoardTemplate("boardStaticPage", vm)
}

func renderServedBoardHTML(result domain.BoardResult) (string, error) {
	return renderServedBoardPage(result, boardPageState{})
}

func renderServedBoardHTMLWithSearchState(result domain.BoardResult, state servedBoardSearchState) (string, error) {
	return renderServedBoardPage(result, boardPageState{Search: state})
}

func renderServedBoardPage(result domain.BoardResult, state boardPageState) (string, error) {
	vm := newBoardServedPageViewModel(result, state)
	return renderBoardTemplate("boardServedPage", vm)
}

// boardPageState is the non-board-input state one served page render needs: the
// search panel state, the write banner a POST/redirect/GET cycle asked for, and
// the CSRF token every write form must embed. A read-only board leaves the
// token empty, and the templates then render no write controls.
type boardPageState struct {
	Search    servedBoardSearchState
	Notice    string
	ErrorCode string
	CSRFToken string
}

// boardPageBanner reads the banner codes a write redirect may carry, accepting
// only codes this adapter produced.
func boardPageBanner(requestURL *url.URL) (string, string) {
	if requestURL == nil {
		return "", ""
	}
	query := requestURL.Query()
	notice := query.Get("notice")
	if _, ok := boardNoticeMessages[notice]; !ok {
		notice = ""
	}
	errorCode := query.Get("error")
	if _, ok := boardErrorMessages[errorCode]; !ok {
		errorCode = ""
	}
	return notice, errorCode
}

// boardBannerMessage maps a notice or error code onto the sentence the page
// shows. Error codes win, so a redirect can never show success next to a
// failure.
func boardBannerMessage(notice, errorCode string) (string, bool) {
	if message, ok := boardErrorMessages[errorCode]; ok {
		return message, true
	}
	if message, ok := boardNoticeMessages[notice]; ok {
		return message, false
	}
	return "", false
}

type servedBoardSearchState struct {
	Query         string
	EntityType    string
	StatusMessage string
	Results       []domain.SearchResult
	HasMore       bool
	Invalid       bool
	Error         bool
}

func renderIssueDetailHTML(detail domain.IssueDetail) (string, error) {
	return renderIssueDetailHTMLWithCSRF(detail, "", "", "")
}

func renderIssueDetailHTMLWithCSRF(detail domain.IssueDetail, csrfToken string, notice string, errorCode string) (string, error) {
	vm := newIssueDetailPageViewModel(detail, csrfToken, notice, errorCode)
	return renderBoardTemplate("boardIssueDetailPage", vm)
}

func renderBoardTemplate(name string, data any) (string, error) {
	var buf bytes.Buffer
	if err := boardTemplates.ExecuteTemplate(&buf, name, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func buildBoardSearchStatusMessage(query string, count int, hasMore bool, invalid bool, searchErr bool) string {
	switch {
	case invalid:
		return "Invalid search query."
	case searchErr:
		return "Search temporarily unavailable."
	case strings.TrimSpace(query) == "":
		return "Initial search: enter a query to find issues, comments, decisions, reviews, and attempt notes."
	case count == 0:
		return fmt.Sprintf("No results found for %q.", query)
	case hasMore:
		return fmt.Sprintf("Showing %d results for %q.", count, query)
	default:
		return fmt.Sprintf("Showing %d result(s) for %q.", count, query)
	}
}

func EffectiveStatusForIssue(detail domain.IssueDetail) domain.EffectiveStatus {
	status, err := domain.EffectiveStatusFor(detail.Issue.Status, detail.LatestAttempt != nil)
	if err != nil {
		return domain.EffectiveStatus(detail.Issue.Status)
	}
	return status
}

func formatIssueDetailTimestamp(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.Format(time.RFC3339)
}

func issueActivitySummary(item domain.ActivityItem) string {
	switch item.EntityType {
	case domain.ActivityEntityTypeComment:
		if item.Comment != nil && strings.TrimSpace(item.Comment.Content) != "" {
			return strings.TrimSpace(item.Comment.Content)
		}
	case domain.ActivityEntityTypeDecision:
		if item.Decision != nil {
			if strings.TrimSpace(item.Decision.Title) != "" {
				return item.Decision.Title
			}
			if strings.TrimSpace(item.Decision.Summary) != "" {
				return item.Decision.Summary
			}
		}
	case domain.ActivityEntityTypeAttempt:
		if item.Attempt != nil {
			if item.Attempt.ResultSummary != nil && strings.TrimSpace(*item.Attempt.ResultSummary) != "" {
				return *item.Attempt.ResultSummary
			}
			if item.Attempt.FailureReasonCode != nil {
				return string(*item.Attempt.FailureReasonCode)
			}
		}
	case domain.ActivityEntityTypeReview:
		if item.Review != nil {
			if strings.TrimSpace(string(item.Review.Status)) != "" {
				return string(item.Review.Status)
			}
		}
	case domain.ActivityEntityTypeEvent:
		if item.Event != nil && item.Event.EventType != "" {
			return item.Event.EventType
		}
	case domain.ActivityEntityTypeArtifact:
		if item.Artifact != nil && item.Artifact.Title != nil && strings.TrimSpace(*item.Artifact.Title) != "" {
			return *item.Artifact.Title
		}
	case domain.ActivityEntityTypeAttemptNote:
		if item.AttemptNote != nil && strings.TrimSpace(item.AttemptNote.Content) != "" {
			return item.AttemptNote.Content
		}
	}
	return string(item.EntityType)
}
