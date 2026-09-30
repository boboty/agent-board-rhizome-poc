package domain

import (
	"sort"
	"strings"
	"time"
)

// BoardWorkflowColumn is one human-facing Agent Board workflow column. The
// columns are a projection of existing issue, attempt, and review state for
// people and agents; they are deliberately not a second persisted status
// store, and they do not replace domain.Status or domain.EffectiveStatus.
type BoardWorkflowColumn string

const (
	// BoardWorkflowColumnReady holds claimable, not-yet-started work.
	BoardWorkflowColumnReady BoardWorkflowColumn = "ready"
	// BoardWorkflowColumnInProgress holds an issue with an active work attempt.
	BoardWorkflowColumnInProgress BoardWorkflowColumn = "in_progress"
	// BoardWorkflowColumnVerifying holds delivery awaiting or undergoing an
	// independent review.
	BoardWorkflowColumnVerifying BoardWorkflowColumn = "verifying"
	// BoardWorkflowColumnRC holds work whose review requested changes and
	// which no developer or verifier has picked up again yet.
	BoardWorkflowColumnRC BoardWorkflowColumn = "rc"
	// BoardWorkflowColumnDecisionRequired holds work stopped by a review
	// outcome of blocked, i.e. waiting on an authoritative human decision.
	BoardWorkflowColumnDecisionRequired BoardWorkflowColumn = "decision_required"
	// BoardWorkflowColumnDone holds completed work.
	BoardWorkflowColumnDone BoardWorkflowColumn = "done"
)

// BoardWorkflowColumns is the board's column order, left to right.
var BoardWorkflowColumns = []BoardWorkflowColumn{
	BoardWorkflowColumnReady,
	BoardWorkflowColumnInProgress,
	BoardWorkflowColumnVerifying,
	BoardWorkflowColumnRC,
	BoardWorkflowColumnDecisionRequired,
	BoardWorkflowColumnDone,
}

// Valid reports whether c is a supported workflow column.
func (c BoardWorkflowColumn) Valid() bool {
	for _, candidate := range BoardWorkflowColumns {
		if c == candidate {
			return true
		}
	}
	return false
}

// Title is the human-facing heading for a column.
func (c BoardWorkflowColumn) Title() string {
	switch c {
	case BoardWorkflowColumnReady:
		return "READY"
	case BoardWorkflowColumnInProgress:
		return "IN PROGRESS"
	case BoardWorkflowColumnVerifying:
		return "VERIFYING"
	case BoardWorkflowColumnRC:
		return "RC"
	case BoardWorkflowColumnDecisionRequired:
		return "DECISION REQUIRED"
	case BoardWorkflowColumnDone:
		return "DONE"
	default:
		return string(c)
	}
}

// Unprojected reason codes. A card that cannot be placed in a workflow column
// honestly is reported with one of these instead of being guessed into a
// column; see DeriveBoardWorkflowPlacement.
const (
	// BoardWorkflowReasonArchived means the issue is archived.
	BoardWorkflowReasonArchived = "archived"
	// BoardWorkflowReasonCancelled means the issue is cancelled.
	BoardWorkflowReasonCancelled = "cancelled"
	// BoardWorkflowReasonNotReady means the issue is stored open, i.e. not yet
	// admitted to the READY queue.
	BoardWorkflowReasonNotReady = "not_ready"
	// BoardWorkflowReasonExternallyBlocked means the issue is stored blocked
	// without a blocked review outcome, so it is an external condition rather
	// than an authoritative decision request.
	BoardWorkflowReasonExternallyBlocked = "externally_blocked"
	// BoardWorkflowReasonUnknownStatus means the stored status is not one this
	// projection knows how to place.
	BoardWorkflowReasonUnknownStatus = "unknown_status"
)

// BoardDeliveryReference is one bounded delivery reference (commit, branch, or
// pull request) recorded against an issue as an artifact. It is shown on a
// card when one exists and omitted otherwise; the board never synthesizes a
// commit reference it did not read.
type BoardDeliveryReference struct {
	Type  ArtifactType `json:"type"`
	URI   string       `json:"uri"`
	Title *string      `json:"title,omitempty"`
}

// BoardWorkflowCard is one issue projected onto one workflow column.
//
// Exactly one card exists per issue and its Column is always a valid workflow
// column, so a task can never appear in two columns at once. Every optional
// field is populated only when the underlying domain data actually carried it;
// consumers render a fallback for absence rather than treating it as an error.
type BoardWorkflowCard struct {
	Column         BoardWorkflowColumn `json:"column"`
	IssueID        string              `json:"issue_id"`
	IssueDisplayID string              `json:"issue_display_id"`
	Title          string              `json:"title"`
	Type           Type                `json:"type"`
	Priority       Priority            `json:"priority"`
	StoredStatus   Status              `json:"stored_status"`
	// Version is the issue's optimistic-concurrency version at read time, so a
	// served write form can round-trip it as expected_version instead of
	// re-reading the issue before every edit.
	Version int64 `json:"version"`

	// READY rank is the issue's optional explicit queue position. It is
	// meaningful only in the READY column and omitted elsewhere.
	ReadyRank   *int64 `json:"ready_rank,omitempty"`
	IsClaimable bool   `json:"is_claimable"`

	// Active-attempt attribution. Populated for IN PROGRESS (a work attempt)
	// and for VERIFYING when a verifier currently holds a review attempt.
	AttemptID           string      `json:"attempt_id,omitempty"`
	AttemptKind         AttemptKind `json:"attempt_kind,omitempty"`
	ExecutorLabel       *string     `json:"executor_label,omitempty"`
	ExecutorInstanceKey *string     `json:"executor_instance_key,omitempty"`
	ExecutorClient      *string     `json:"executor_client,omitempty"`
	ExecutorModel       *string     `json:"executor_model,omitempty"`
	ExecutorWorktree    *string     `json:"executor_worktree,omitempty"`
	AttemptStartedAt    *time.Time  `json:"attempt_started_at,omitempty"`
	LeaseExpiresAt      *time.Time  `json:"lease_expires_at,omitempty"`

	// Review signal. Populated from the issue's newest review request across
	// every status the board reads (any status except superseded, whose
	// successor is always newer and read) whenever one exists, so VERIFYING,
	// RC, DECISION REQUIRED and a reopened READY card all carry the last
	// recorded decision rather than an obsolete round.
	ReviewRequestID     *string              `json:"review_request_id,omitempty"`
	ReviewStatus        *ReviewRequestStatus `json:"review_status,omitempty"`
	ReviewTargetVersion *int64               `json:"review_target_version,omitempty"`
	ReviewRequestedAt   *time.Time           `json:"review_requested_at,omitempty"`
	ReviewResolvedAt    *time.Time           `json:"review_resolved_at,omitempty"`
	// ChangesRequestedCount is how many review requests the board read for
	// this issue resolved to changes_requested, i.e. the number of failed
	// verification rounds observed. It is a lower bound when the board's
	// review reads were truncated (see Truncation.ReviewRequests). The free
	// text a reviewer wrote when requesting changes lives on the review
	// outcome record, which the board's bounded review read does not carry.
	ChangesRequestedCount int `json:"changes_requested_count,omitempty"`

	// Delivery references recorded as artifacts on the issue.
	Delivery []BoardDeliveryReference `json:"delivery,omitempty"`
}

// BoardWorkflowUnprojected is one issue the projection deliberately did not
// place in a workflow column, with the reason why. It keeps the board honest:
// work that no V0.1 column describes is reported rather than misfiled.
type BoardWorkflowUnprojected struct {
	IssueID        string `json:"issue_id"`
	IssueDisplayID string `json:"issue_display_id"`
	Title          string `json:"title"`
	StoredStatus   Status `json:"stored_status"`
	// Version is the issue's optimistic-concurrency version at read time, for
	// the same reason BoardWorkflowCard carries it.
	Version int64  `json:"version"`
	Reason  string `json:"reason"`
	Detail  string `json:"detail"`
}

// BoardWorkflowColumnSummary is one column's heading and card count, always
// present for every column so a UI renders empty columns without inferring
// them from card data.
type BoardWorkflowColumnSummary struct {
	Column BoardWorkflowColumn `json:"column"`
	Title  string              `json:"title"`
	Count  int                 `json:"count"`
}

// BoardWorkflowTruncation reports, per column, whether the bounded read that
// fed it was cut. A true flag means "only the first MaxBoardCollectionLimit
// contributing issues are represented", so a card count can be a lower bound.
type BoardWorkflowTruncation struct {
	Ready          bool `json:"ready"`
	Verifying      bool `json:"verifying"`
	Done           bool `json:"done"`
	Unprojected    bool `json:"unprojected"`
	ReviewRequests bool `json:"review_requests"`
	// DeliveryOverflow reports that a card had more delivery references than
	// the card limit, so its list is a prefix.
	DeliveryOverflow bool `json:"delivery_overflow"`
	// DeliveryUnavailable reports that at least one card's delivery
	// references could not be read. The card stays on the board without them
	// rather than failing the whole projection, because a delivery reference
	// is supplementary information.
	DeliveryUnavailable bool `json:"delivery_unavailable"`
}

// Any reports whether any contributing read was cut or degraded.
func (t BoardWorkflowTruncation) Any() bool {
	return t.Ready || t.Verifying || t.Done || t.Unprojected || t.ReviewRequests ||
		t.DeliveryOverflow || t.DeliveryUnavailable
}

// BoardWorkflowProjection is the read-only Kanban projection of project state.
// It is derived on every read from issue, attempt, and review data; nothing
// here is persisted.
type BoardWorkflowProjection struct {
	Columns     []BoardWorkflowColumnSummary `json:"columns"`
	Cards       []BoardWorkflowCard          `json:"cards"`
	Unprojected []BoardWorkflowUnprojected   `json:"unprojected"`
	Truncation  BoardWorkflowTruncation      `json:"truncation"`
}

// BoardWorkflowPlacementInput is the derivation input for one issue.
type BoardWorkflowPlacementInput struct {
	Issue Issue
	// ActiveAttempt is the issue's active, unexpired attempt, if any.
	ActiveAttempt *ActiveAttemptSummary
	// LatestReview is the issue's newest review request among the states the
	// board reads, if any. Its Status is what distinguishes RC (changes
	// requested) from DECISION REQUIRED (blocked) from an ordinary READY card:
	// a newer approved or cancelled request must not be shadowed by an older
	// changes_requested or blocked one.
	LatestReview *ReviewRequest
}

// DeriveBoardWorkflowPlacement maps one issue onto at most one workflow
// column. It returns the column and an empty reason when the issue belongs on
// the board, or an empty column and a BoardWorkflowReason* code when it does
// not. It is pure: it reads the supplied projection and never queries, so the
// board's column assignment cannot drift from the projection rules.
//
// The rules, in order, are:
//
//  1. archived -> unprojected (archived).
//  2. done -> DONE.
//  3. cancelled -> unprojected (cancelled).
//  4. an active attempt: a work attempt -> IN PROGRESS, a review attempt ->
//     VERIFYING. This is checked before the stored status because a claimed
//     issue keeps its stored status while its effective status is derived.
//  5. stored review -> VERIFYING (delivery is available for review).
//  6. stored blocked -> DECISION REQUIRED when the latest review resolved to
//     blocked, otherwise unprojected as an external block. A blocked issue is
//     never shown in RC: changes_requested moves an issue to ready, so a
//     blocked issue carrying one was blocked again afterwards.
//  7. stored ready -> RC when the latest review resolved to
//     changes_requested, otherwise READY.
//  8. anything else -> unprojected with a reason.
//
// The stored status is the spine, and the review signal only refines the two
// states a review outcome can actually produce (ready after
// changes_requested, blocked after blocked). A review state that no rule
// claims cannot move a card out of the column its stored status implies.
func DeriveBoardWorkflowPlacement(input BoardWorkflowPlacementInput) (BoardWorkflowColumn, string) {
	issue := input.Issue
	if issue.ArchivedAt != nil {
		return "", BoardWorkflowReasonArchived
	}
	switch issue.Status {
	case StatusDone:
		return BoardWorkflowColumnDone, ""
	case StatusCancelled:
		return "", BoardWorkflowReasonCancelled
	}
	if input.ActiveAttempt != nil {
		switch input.ActiveAttempt.Kind {
		case AttemptKindWork:
			return BoardWorkflowColumnInProgress, ""
		case AttemptKindReview:
			return BoardWorkflowColumnVerifying, ""
		}
	}
	switch issue.Status {
	case StatusReview:
		return BoardWorkflowColumnVerifying, ""
	case StatusBlocked:
		if input.LatestReview != nil && input.LatestReview.Status == ReviewRequestStatusBlocked {
			return BoardWorkflowColumnDecisionRequired, ""
		}
		return "", BoardWorkflowReasonExternallyBlocked
	case StatusReady:
		if input.LatestReview != nil && input.LatestReview.Status == ReviewRequestStatusChangesRequested {
			return BoardWorkflowColumnRC, ""
		}
		return BoardWorkflowColumnReady, ""
	case StatusOpen:
		return "", BoardWorkflowReasonNotReady
	default:
		return "", BoardWorkflowReasonUnknownStatus
	}
}

// BoardWorkflowUnprojectedDetail renders the human-facing sentence for an
// unprojected reason code.
func BoardWorkflowUnprojectedDetail(reason string) string {
	switch reason {
	case BoardWorkflowReasonArchived:
		return "Archived; not part of the active workflow board."
	case BoardWorkflowReasonCancelled:
		return "Cancelled; not part of the active workflow board."
	case BoardWorkflowReasonNotReady:
		return "Stored open: not yet admitted to the READY queue."
	case BoardWorkflowReasonExternallyBlocked:
		return "Stored blocked without a blocked review outcome: an external condition, not an authoritative decision request."
	case BoardWorkflowReasonUnknownStatus:
		return "Stored status is not part of the Agent Board workflow projection."
	default:
		return "Not placed in a workflow column."
	}
}

// NewBoardWorkflowProjection assembles the projection from already-derived
// cards, counts every column (including empty ones), and sorts cards into a
// deterministic order: column order first, then READY rank (unranked last),
// then priority, then issue sequence.
func NewBoardWorkflowProjection(cards []BoardWorkflowCard, unprojected []BoardWorkflowUnprojected, truncation BoardWorkflowTruncation) BoardWorkflowProjection {
	sorted := make([]BoardWorkflowCard, len(cards))
	copy(sorted, cards)
	sort.SliceStable(sorted, func(i, j int) bool {
		leftRank, rightRank := columnOrder(sorted[i].Column), columnOrder(sorted[j].Column)
		if leftRank != rightRank {
			return leftRank < rightRank
		}
		// The READY column uses the one shared queue comparator (ready_queue.go),
		// so display order and reorder planning can never disagree.
		if sorted[i].Column == BoardWorkflowColumnReady {
			return compareReadyCards(sorted[i], sorted[j]) < 0
		}
		leftPriority, rightPriority := priorityOrder(sorted[i].Priority), priorityOrder(sorted[j].Priority)
		if leftPriority != rightPriority {
			return leftPriority < rightPriority
		}
		return strings.TrimSpace(sorted[i].IssueDisplayID) < strings.TrimSpace(sorted[j].IssueDisplayID)
	})
	projection := BoardWorkflowProjection{
		Columns:     make([]BoardWorkflowColumnSummary, 0, len(BoardWorkflowColumns)),
		Cards:       sorted,
		Unprojected: unprojected,
		Truncation:  truncation,
	}
	if projection.Cards == nil {
		projection.Cards = []BoardWorkflowCard{}
	}
	if projection.Unprojected == nil {
		projection.Unprojected = []BoardWorkflowUnprojected{}
	}
	counts := make(map[BoardWorkflowColumn]int, len(BoardWorkflowColumns))
	for _, card := range sorted {
		counts[card.Column]++
	}
	for _, column := range BoardWorkflowColumns {
		projection.Columns = append(projection.Columns, BoardWorkflowColumnSummary{
			Column: column, Title: column.Title(), Count: counts[column],
		})
	}
	return projection
}

func columnOrder(column BoardWorkflowColumn) int {
	for index, candidate := range BoardWorkflowColumns {
		if column == candidate {
			return index
		}
	}
	return len(BoardWorkflowColumns)
}

func priorityOrder(priority Priority) int {
	switch priority {
	case PriorityCritical:
		return 0
	case PriorityHigh:
		return 1
	case PriorityMedium:
		return 2
	case PriorityLow:
		return 3
	default:
		return 4
	}
}
