package cli

import (
	"fmt"
	"strings"
	"time"

	"rhizome-mcp/internal/domain"
)

// BoardWorkflow is the stable CLI/HTTP projection of the board's workflow
// (Kanban) view: the four task-level columns with their card counts, one card
// per projected issue, and the issues the projection deliberately did not
// place. It is derived from the board's own reads and persists nothing.
type BoardWorkflow struct {
	Columns     []BoardWorkflowColumn      `json:"columns"`
	Cards       []BoardWorkflowCard        `json:"cards"`
	Unprojected []BoardWorkflowUnprojected `json:"unprojected"`
	Truncation  BoardWorkflowTruncation    `json:"truncation"`
}

// BoardWorkflowColumn is one column heading and its card count.
type BoardWorkflowColumn struct {
	Column string `json:"column"`
	Title  string `json:"title"`
	Count  int    `json:"count"`
}

// BoardWorkflowCard is one issue projected onto one workflow column. Every
// optional field is omitted when the underlying domain data did not carry it,
// so a client renders a fallback rather than a guessed value.
type BoardWorkflowCard struct {
	Column         string `json:"column"`
	IssueID        string `json:"issue_id"`
	IssueDisplayID string `json:"issue_display_id"`
	Title          string `json:"title"`
	Type           string `json:"type"`
	Priority       string `json:"priority"`
	StoredStatus   string `json:"stored_status"`
	// Version is the issue's optimistic version, so a client that edits a card
	// (the served board's forms do) can round-trip expected_version without a
	// separate read.
	Version int64 `json:"version"`

	ReadyRank   *int64 `json:"ready_rank,omitempty"`
	IsClaimable bool   `json:"is_claimable"`

	AttemptID           string     `json:"attempt_id,omitempty"`
	AttemptKind         string     `json:"attempt_kind,omitempty"`
	ExecutorLabel       *string    `json:"executor_label,omitempty"`
	ExecutorInstanceKey *string    `json:"executor_instance_key,omitempty"`
	ExecutorClient      *string    `json:"executor_client,omitempty"`
	ExecutorModel       *string    `json:"executor_model,omitempty"`
	ExecutorWorktree    *string    `json:"executor_worktree,omitempty"`
	AttemptStartedAt    *time.Time `json:"attempt_started_at,omitempty"`
	LeaseExpiresAt      *time.Time `json:"lease_expires_at,omitempty"`

	BlockedReason         *string    `json:"blocked_reason,omitempty"`
	ReviewRequestID       *string    `json:"review_request_id,omitempty"`
	ReviewStatus          *string    `json:"review_status,omitempty"`
	ReviewTargetVersion   *int64     `json:"review_target_version,omitempty"`
	ReviewRequestedAt     *time.Time `json:"review_requested_at,omitempty"`
	ReviewResolvedAt      *time.Time `json:"review_resolved_at,omitempty"`
	ChangesRequestedCount int        `json:"changes_requested_count,omitempty"`

	Delivery []BoardWorkflowDelivery `json:"delivery,omitempty"`
}

// BoardWorkflowDelivery is one commit, branch, or pull-request reference
// recorded against the card's issue.
type BoardWorkflowDelivery struct {
	Type  string  `json:"type"`
	URI   string  `json:"uri"`
	Title *string `json:"title,omitempty"`
}

// BoardWorkflowUnprojected is one issue the projection did not place in a
// column, with the machine-readable reason and a human sentence.
type BoardWorkflowUnprojected struct {
	IssueID        string `json:"issue_id"`
	IssueDisplayID string `json:"issue_display_id"`
	Title          string `json:"title"`
	StoredStatus   string `json:"stored_status"`
	Version        int64  `json:"version"`
	Reason         string `json:"reason"`
	Detail         string `json:"detail"`
}

// BoardWorkflowTruncation reports whether a bounded read that fed the
// projection was cut, per contributing source.
type BoardWorkflowTruncation struct {
	Ready               bool `json:"ready"`
	Review              bool `json:"review"`
	Blocked             bool `json:"blocked"`
	Done                bool `json:"done"`
	Unprojected         bool `json:"unprojected"`
	ReviewRequests      bool `json:"review_requests"`
	DeliveryOverflow    bool `json:"delivery_overflow"`
	DeliveryUnavailable bool `json:"delivery_unavailable"`
}

func boardWorkflowFromDomain(workflow domain.BoardWorkflowProjection) BoardWorkflow {
	columns := make([]BoardWorkflowColumn, len(workflow.Columns))
	for index, column := range workflow.Columns {
		columns[index] = BoardWorkflowColumn{Column: string(column.Column), Title: column.Title, Count: column.Count}
	}
	cards := make([]BoardWorkflowCard, len(workflow.Cards))
	for index, card := range workflow.Cards {
		cards[index] = BoardWorkflowCard{
			Column: string(card.Column), IssueID: card.IssueID, IssueDisplayID: card.IssueDisplayID,
			Title: card.Title, Type: string(card.Type), Priority: string(card.Priority),
			StoredStatus: string(card.StoredStatus), Version: card.Version, ReadyRank: copyOptionalInt64(card.ReadyRank),
			BlockedReason: copyOptionalString(card.BlockedReason),
			IsClaimable:   card.IsClaimable, AttemptID: card.AttemptID, AttemptKind: string(card.AttemptKind),
			ExecutorLabel: copyOptionalString(card.ExecutorLabel), ExecutorInstanceKey: copyOptionalString(card.ExecutorInstanceKey),
			ExecutorClient: copyOptionalString(card.ExecutorClient), ExecutorModel: copyOptionalString(card.ExecutorModel),
			ExecutorWorktree: copyOptionalString(card.ExecutorWorktree),
			AttemptStartedAt: copyOptionalTime(card.AttemptStartedAt), LeaseExpiresAt: copyOptionalTime(card.LeaseExpiresAt),
			ReviewRequestID: copyOptionalString(card.ReviewRequestID), ReviewTargetVersion: copyOptionalInt64(card.ReviewTargetVersion),
			ReviewRequestedAt: copyOptionalTime(card.ReviewRequestedAt), ReviewResolvedAt: copyOptionalTime(card.ReviewResolvedAt),
			ChangesRequestedCount: card.ChangesRequestedCount,
			Delivery:              boardWorkflowDeliveryFromDomain(card.Delivery),
		}
		if card.ReviewStatus != nil {
			status := string(*card.ReviewStatus)
			cards[index].ReviewStatus = &status
		}
	}
	unprojected := make([]BoardWorkflowUnprojected, len(workflow.Unprojected))
	for index, item := range workflow.Unprojected {
		unprojected[index] = BoardWorkflowUnprojected{
			IssueID: item.IssueID, IssueDisplayID: item.IssueDisplayID, Title: item.Title,
			StoredStatus: string(item.StoredStatus), Version: item.Version, Reason: item.Reason, Detail: item.Detail,
		}
	}
	return BoardWorkflow{
		Columns: columns, Cards: cards, Unprojected: unprojected,
		Truncation: BoardWorkflowTruncation{
			Ready:               workflow.Truncation.Ready,
			Review:              workflow.Truncation.Review,
			Blocked:             workflow.Truncation.Blocked,
			Done:                workflow.Truncation.Done,
			Unprojected:         workflow.Truncation.Unprojected,
			ReviewRequests:      workflow.Truncation.ReviewRequests,
			DeliveryOverflow:    workflow.Truncation.DeliveryOverflow,
			DeliveryUnavailable: workflow.Truncation.DeliveryUnavailable,
		},
	}
}

func boardWorkflowDeliveryFromDomain(references []domain.BoardDeliveryReference) []BoardWorkflowDelivery {
	if len(references) == 0 {
		return nil
	}
	delivery := make([]BoardWorkflowDelivery, len(references))
	for index, reference := range references {
		delivery[index] = BoardWorkflowDelivery{
			Type: string(reference.Type), URI: reference.URI, Title: copyOptionalString(reference.Title),
		}
	}
	return delivery
}

// writeBoardWorkflowTable renders the workflow projection as a TSV section of
// the CLI board table. Cards are listed in board order (the projection already
// sorts them), then the issues that were deliberately not placed, so a reader
// can see that nothing was dropped silently.
func writeBoardWorkflowTable(builder *strings.Builder, workflow domain.BoardWorkflowProjection) {
	builder.WriteString("\nworkflow\n")
	builder.WriteString("column\ttitle\tcount\n")
	for _, column := range workflow.Columns {
		builder.WriteString(fmt.Sprintf("%s\t%s\t%d\n", column.Column, column.Title, column.Count))
	}
	builder.WriteString("\nworkflow_cards\n")
	builder.WriteString("column\tissue\ttitle\tpriority\tready_rank\tattempt\texecutor\tclient\tmodel\tworktree\treview_status\tchanges_requested\tblocked_reason\tcommit\n")
	for _, card := range workflow.Cards {
		readyRank := ""
		if card.ReadyRank != nil {
			readyRank = fmt.Sprintf("%d", *card.ReadyRank)
		}
		reviewStatus := ""
		if card.ReviewStatus != nil {
			reviewStatus = string(*card.ReviewStatus)
		}
		changesRequested := ""
		if card.ChangesRequestedCount > 0 {
			changesRequested = fmt.Sprintf("%d", card.ChangesRequestedCount)
		}
		builder.WriteString(fmt.Sprintf("%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			card.Column, card.IssueDisplayID, escapeTableValue(card.Title), card.Priority, readyRank,
			card.AttemptKind, escapeTableValue(optionalTableValue(card.ExecutorLabel)),
			escapeTableValue(optionalTableValue(card.ExecutorClient)), escapeTableValue(optionalTableValue(card.ExecutorModel)),
			escapeTableValue(optionalTableValue(card.ExecutorWorktree)), reviewStatus, changesRequested,
			escapeTableValue(optionalTableValue(card.BlockedReason)),
			escapeTableValue(boardWorkflowDeliveryCell(card.Delivery))))
	}
	if workflow.Truncation.Ready {
		builder.WriteString(fmt.Sprintf("truncated\ttrue\t(READY cards cut at %d)\n", domain.MaxBoardCollectionLimit))
	}
	if workflow.Truncation.Review {
		builder.WriteString(fmt.Sprintf("truncated\ttrue\t(IN PROGRESS cards from stored review cut at %d)\n", domain.MaxBoardCollectionLimit))
	}
	if workflow.Truncation.Blocked {
		builder.WriteString(fmt.Sprintf("truncated\ttrue\t(BLOCKED cards cut at %d)\n", domain.MaxBoardCollectionLimit))
	}
	if workflow.Truncation.Done {
		builder.WriteString(fmt.Sprintf("truncated\ttrue\t(DONE cards cut at %d)\n", domain.MaxBoardCollectionLimit))
	}
	if workflow.Truncation.ReviewRequests {
		builder.WriteString(fmt.Sprintf("truncated\ttrue\t(review signals cut at %d)\n", domain.MaxBoardCollectionLimit))
	}

	builder.WriteString("\nworkflow_unprojected\n")
	builder.WriteString("issue\tstored_status\treason\tdetail\n")
	for _, item := range workflow.Unprojected {
		builder.WriteString(fmt.Sprintf("%s\t%s\t%s\t%s\n",
			item.IssueDisplayID, item.StoredStatus, item.Reason, escapeTableValue(item.Detail)))
	}
	if workflow.Truncation.Unprojected {
		builder.WriteString(fmt.Sprintf("truncated\ttrue\t(first %d shown)\n", domain.MaxBoardCollectionLimit))
	}
	if workflow.Truncation.DeliveryOverflow {
		builder.WriteString("truncated\ttrue\t(a card shows fewer delivery references than the issue has)\n")
	}
	if workflow.Truncation.DeliveryUnavailable {
		builder.WriteString("truncated\ttrue\t(a card's delivery references could not be read)\n")
	}
}

func boardWorkflowDeliveryCell(references []domain.BoardDeliveryReference) string {
	if len(references) == 0 {
		return ""
	}
	parts := make([]string, 0, len(references))
	for _, reference := range references {
		parts = append(parts, string(reference.Type)+" "+reference.URI)
	}
	return strings.Join(parts, "; ")
}
