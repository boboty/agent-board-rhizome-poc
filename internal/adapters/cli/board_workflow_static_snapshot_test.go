package cli

import (
	"strings"
	"testing"

	"rhizome-mcp/internal/domain"
)

// workflowSnapshotBoard builds a board whose workflow projection is populated,
// which is what the pre-existing static-snapshot coverage test never did: its
// fixture left Workflow zero-valued, so the projection's own rendering was
// never checked against the snapshot's self-containment contract.
func workflowSnapshotBoard() domain.BoardResult {
	rank := int64(1)
	title := "feat: agent board kanban"
	return domain.BoardResult{Workflow: domain.NewBoardWorkflowProjection([]domain.BoardWorkflowCard{
		{
			Column: domain.BoardWorkflowColumnReady, IssueID: "issue-1", IssueDisplayID: "ISSUE-1",
			Title: "Ready work", Priority: domain.PriorityHigh, ReadyRank: &rank,
		},
		{
			Column: domain.BoardWorkflowColumnDone, IssueID: "issue-2", IssueDisplayID: "ISSUE-2",
			Title: "Shipped work", Priority: domain.PriorityLow,
			Delivery: []domain.BoardDeliveryReference{{Type: domain.ArtifactTypeCommit, URI: "abc1234", Title: &title}},
		},
	}, []domain.BoardWorkflowUnprojected{{
		IssueID: "issue-3", IssueDisplayID: "ISSUE-3", Title: "Not ready", StoredStatus: domain.StatusOpen,
		Reason: domain.BoardWorkflowReasonNotReady, Detail: domain.BoardWorkflowUnprojectedDetail(domain.BoardWorkflowReasonNotReady),
	}}, domain.BoardWorkflowTruncation{DeliveryOverflow: true})}
}

// TestBoardWorkflowStaticSnapshotStaysSelfContained guards the offline
// snapshot contract (board_html.go): the static file serves no routes, so a
// Kanban card must not link to one. The projection is rendered by a view model
// shared with the served page, which is exactly how this leaked before.
func TestBoardWorkflowStaticSnapshotStaysSelfContained(t *testing.T) {
	html, err := renderBoardHTML(workflowSnapshotBoard())
	if err != nil {
		t.Fatalf("renderBoardHTML: %v", err)
	}
	if strings.Contains(html, "/issues/") {
		t.Fatalf("static board leaked an issue route:\n%s", html)
	}
	if !strings.Contains(html, `data-issue="ISSUE-1"`) || !strings.Contains(html, "Ready work") {
		t.Fatalf("static board is missing its workflow cards:\n%s", html)
	}
	if strings.Contains(html, "data-board-search-form") {
		t.Fatalf("static board included served-only search UI")
	}
}

// TestBoardWorkflowServedBoardLinksIssues is the other half: the served board
// does serve /issues/{id}, so its cards link to the issue page.
func TestBoardWorkflowServedBoardLinksIssues(t *testing.T) {
	html, err := renderServedBoardHTMLWithSearchState(workflowSnapshotBoard(), servedBoardSearchState{})
	if err != nil {
		t.Fatalf("renderServedBoardHTMLWithSearchState: %v", err)
	}
	if !strings.Contains(html, `href="/issues/ISSUE-1"`) {
		t.Fatalf("served board did not link its workflow card:\n%s", html)
	}
	if !strings.Contains(html, `href="/issues/ISSUE-3"`) {
		t.Fatalf("served board did not link its unprojected issue:\n%s", html)
	}
}

// TestBoardWorkflowUnknownAttemptKindDegrades pins that an attempt kind this
// board does not recognise is labelled neutrally rather than being attributed
// to a developer or a verifier the data never named.
func TestBoardWorkflowUnknownAttemptKindDegrades(t *testing.T) {
	label := "whoever"
	board := domain.BoardResult{Workflow: domain.NewBoardWorkflowProjection([]domain.BoardWorkflowCard{{
		Column: domain.BoardWorkflowColumnInProgress, IssueID: "issue-1", IssueDisplayID: "ISSUE-1",
		Title: "Odd kind", Priority: domain.PriorityLow, AttemptID: "attempt-1",
		AttemptKind: domain.AttemptKind("mystery"), ExecutorLabel: &label,
	}}, nil, domain.BoardWorkflowTruncation{})}
	html, err := renderServedBoardHTML(board)
	if err != nil {
		t.Fatalf("renderServedBoardHTML: %v", err)
	}
	if strings.Contains(html, "开发者") || strings.Contains(html, "验证者") {
		t.Fatalf("unknown attempt kind was attributed a role:\n%s", html)
	}
	if !strings.Contains(html, "执行者") {
		t.Fatalf("unknown attempt kind did not degrade to the neutral role:\n%s", html)
	}
}

// TestBoardWorkflowTableMarksDeliveryOverflow keeps the CLI table's truncation
// convention: a cut collection is marked, not silently shortened.
func TestBoardWorkflowTableMarksDeliveryOverflow(t *testing.T) {
	var builder strings.Builder
	writeBoardWorkflowTable(&builder, workflowSnapshotBoard().Workflow)
	output := builder.String()
	if !strings.Contains(output, "\nworkflow\n") || !strings.Contains(output, "\nworkflow_cards\n") {
		t.Fatalf("workflow sections missing:\n%s", output)
	}
	headerAt := strings.Index(output, "column\tissue\ttitle")
	if headerAt < 0 {
		t.Fatalf("workflow_cards header missing:\n%s", output)
	}
	if !strings.Contains(output[headerAt:], "delivery reference") {
		t.Fatalf("delivery overflow was not marked:\n%s", output)
	}
}

// TestBoardWorkflowTruncationFlagsReachEverySurface guards the wiring from the
// domain truncation object through the CLI/JSON projection, the table markers,
// and both HTML views. The HTML view model once missed the newer flags, which
// made template rendering fail outright, so each flag is asserted everywhere.
func TestBoardWorkflowTruncationFlagsReachEverySurface(t *testing.T) {
	rank := int64(1)
	board := domain.BoardResult{Workflow: domain.NewBoardWorkflowProjection(
		[]domain.BoardWorkflowCard{{
			Column: domain.BoardWorkflowColumnReady, IssueID: "issue-1", IssueDisplayID: "ISSUE-1",
			Title: "Ready work", Priority: domain.PriorityHigh, ReadyRank: &rank,
		}},
		[]domain.BoardWorkflowUnprojected{},
		domain.BoardWorkflowTruncation{
			Ready: true, Review: true, Blocked: true, Done: true, Unprojected: true,
			ReviewRequests: true, DeliveryOverflow: true, DeliveryUnavailable: true,
		},
	)}

	if !board.Workflow.Truncation.Any() {
		t.Fatal("Truncation.Any() = false with every flag set")
	}

	var builder strings.Builder
	writeBoardWorkflowTable(&builder, board.Workflow)
	table := builder.String()
	for _, want := range []string{
		"READY cards cut", "IN PROGRESS cards from stored review cut", "BLOCKED cards cut",
		"DONE cards cut", "review signals cut",
		"first 100 shown", "fewer delivery references", "could not be read",
	} {
		if !strings.Contains(table, want) {
			t.Fatalf("table is missing truncation marker %q:\n%s", want, table)
		}
	}

	staticHTML, err := renderBoardHTML(board)
	if err != nil {
		t.Fatalf("renderBoardHTML: %v", err)
	}
	servedHTML, err := renderServedBoardHTML(board)
	if err != nil {
		t.Fatalf("renderServedBoardHTML: %v", err)
	}
	for name, html := range map[string]string{"static": staticHTML, "served": servedHTML} {
		for _, want := range []string{
			"待开始卡片已截断", "进行中卡片已截断",
			"已阻塞卡片已截断", "已完成卡片已截断",
			"验收信号已截断", "交付引用已截断", "交付引用无法读取",
		} {
			if !strings.Contains(html, want) {
				t.Fatalf("%s HTML is missing truncation note %q", name, want)
			}
		}
	}

	response := boardResponseFromDomain(board)
	truncation := response.Workflow.Truncation
	if !truncation.Ready || !truncation.Review || !truncation.Blocked || !truncation.Done ||
		!truncation.Unprojected || !truncation.ReviewRequests || !truncation.DeliveryOverflow ||
		!truncation.DeliveryUnavailable {
		t.Fatalf("JSON truncation = %#v, want every flag set", truncation)
	}
}
