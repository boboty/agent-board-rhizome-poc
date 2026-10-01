package sqlite_test

import (
	"context"
	"reflect"
	"testing"

	"rhizome-mcp/internal/domain"
)

// readyQueue ranks are explicit queue positions, so the READY queue must read
// back in rank order with a deterministic tie-break, and a rank stored on a
// task that is not currently ready must not move it.
func TestIssueReadyRankPersistsAndOrdersReadyQueue(t *testing.T) {
	service, _, _ := openIssueService(t)
	ctx := context.Background()

	create := func(title string, status domain.Status, rank *int64) domain.Issue {
		t.Helper()
		issue, err := service.CreateIssue(ctx, domain.CreateIssueInput{
			Type: domain.TypeTask, Title: title, Status: status, ReadyRank: rank,
		})
		if err != nil {
			t.Fatalf("create %s: %v", title, err)
		}
		return issue.Issue
	}

	rankTen := int64(10)
	rankTwenty := int64(20)
	rankOne := int64(1)
	late := create("late-ranked", domain.StatusReady, &rankTwenty)
	unranked := create("unranked", domain.StatusReady, nil)
	first := create("first-ranked", domain.StatusReady, &rankTen)
	second := create("second-ranked", domain.StatusReady, &rankTen)
	notReady := create("open-but-ranked", domain.StatusOpen, &rankOne)

	page, err := service.ListIssues(ctx, domain.ListIssuesInput{Statuses: []domain.Status{domain.StatusReady}})
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, len(page.Items))
	for index, item := range page.Items {
		got[index] = item.DisplayID
	}
	want := []string{first.DisplayID, second.DisplayID, late.DisplayID, unranked.DisplayID}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("READY queue order = %v, want %v", got, want)
	}
	if page.HasMore || page.NextCursor != nil {
		t.Fatalf("single-page READY queue = %#v", page)
	}

	// Rank values round-trip through the detail projection, and the non-ready
	// issue keeps its stored (but currently inert) rank.
	detail, err := service.GetIssueProjection(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.ReadyRank == nil || *detail.ReadyRank != rankTen {
		t.Fatalf("ready rank = %v, want %d", detail.ReadyRank, rankTen)
	}
	openDetail, err := service.GetIssueProjection(ctx, notReady.ID)
	if err != nil {
		t.Fatal(err)
	}
	if openDetail.ReadyRank == nil || *openDetail.ReadyRank != 1 {
		t.Fatalf("non-ready stored rank = %v, want 1", openDetail.ReadyRank)
	}
	if unrankedItem := findIssue(t, page.Items, unranked.ID); unrankedItem.ReadyRank != nil {
		t.Fatalf("unranked issue rank = %v, want nil", *unrankedItem.ReadyRank)
	}

	// Unfiltered ordering also ignores the rank of a non-ready issue: the
	// ranked READY tasks lead, and the ranked open task keeps the ordinary
	// priority/sequence position behind them.
	unfiltered, err := service.ListIssues(ctx, domain.ListIssuesInput{})
	if err != nil {
		t.Fatal(err)
	}
	if indexOfIssue(unfiltered.Items, late.ID) > indexOfIssue(unfiltered.Items, notReady.ID) {
		t.Fatalf("ranked READY issue did not lead the unfiltered page: %v", issueIDs(unfiltered.Items))
	}
}

func TestIssueReadyRankCursorPaginationIsStable(t *testing.T) {
	service, _, _ := openIssueService(t)
	ctx := context.Background()

	ranks := []int64{30, 10, 50, 10, 20, 40}
	for index, rank := range ranks {
		value := rank
		if _, err := service.CreateIssue(ctx, domain.CreateIssueInput{
			Type: domain.TypeTask, Title: "queued", Status: domain.StatusReady, ReadyRank: &value,
		}); err != nil {
			t.Fatalf("create %d: %v", index, err)
		}
	}
	// One unranked READY task, which must sort after every ranked one.
	if _, err := service.CreateIssue(ctx, domain.CreateIssueInput{
		Type: domain.TypeTask, Title: "unranked", Status: domain.StatusReady,
	}); err != nil {
		t.Fatal(err)
	}

	full, err := service.ListIssues(ctx, domain.ListIssuesInput{Statuses: []domain.Status{domain.StatusReady}, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(full.Items) != len(ranks)+1 {
		t.Fatalf("full page has %d items, want %d", len(full.Items), len(ranks)+1)
	}
	want := issueIDs(full.Items)
	if full.Items[0].ReadyRank == nil || *full.Items[0].ReadyRank != 10 {
		t.Fatalf("expected the lowest rank first, got %v", full.Items[0].ReadyRank)
	}
	if full.Items[len(full.Items)-1].ReadyRank != nil {
		t.Fatalf("unranked task did not sort last: %v", full.Items[len(full.Items)-1].ReadyRank)
	}

	var traversed []string
	var cursor string
	for {
		page, err := service.ListIssues(ctx, domain.ListIssuesInput{
			Statuses: []domain.Status{domain.StatusReady}, Limit: 2, Cursor: cursor,
		})
		if err != nil {
			t.Fatalf("cursor %q: %v", cursor, err)
		}
		traversed = append(traversed, issueIDs(page.Items)...)
		if !page.HasMore {
			break
		}
		if page.NextCursor == nil {
			t.Fatal("HasMore page without a cursor")
		}
		cursor = *page.NextCursor
	}
	if !reflect.DeepEqual(traversed, want) {
		t.Fatalf("cursor traversal = %v, want %v", traversed, want)
	}
}

// TestIssueReadyRankCursorCrossesTheUnrankedBoundary walks the unfiltered list,
// where pages must transition from ranked READY issues to the unranked
// sentinel bucket (and on to non-ready issues) without dropping or repeating a
// row. Drafting the cursor from the last ranked item is exactly where a
// keyset predicate that compared only the residual keys would lose rows.
func TestIssueReadyRankCursorCrossesTheUnrankedBoundary(t *testing.T) {
	service, _, _ := openIssueService(t)
	ctx := context.Background()

	// Two ranked READY issues, so a limit of 2 puts the page boundary exactly
	// on the ranked -> unranked transition.
	rankLow, rankHigh := int64(5), int64(6)
	for _, rank := range []*int64{&rankLow, &rankHigh} {
		if _, err := service.CreateIssue(ctx, domain.CreateIssueInput{
			Type: domain.TypeTask, Title: "ranked", Status: domain.StatusReady, ReadyRank: rank,
		}); err != nil {
			t.Fatal(err)
		}
	}
	for index := 0; index < 3; index++ {
		if _, err := service.CreateIssue(ctx, domain.CreateIssueInput{
			Type: domain.TypeTask, Title: "unranked ready", Status: domain.StatusReady,
		}); err != nil {
			t.Fatal(err)
		}
	}
	for index := 0; index < 2; index++ {
		if _, err := service.CreateIssue(ctx, domain.CreateIssueInput{
			Type: domain.TypeTask, Title: "open", Status: domain.StatusOpen,
		}); err != nil {
			t.Fatal(err)
		}
	}

	full, err := service.ListIssues(ctx, domain.ListIssuesInput{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(full.Items) != 7 {
		t.Fatalf("full page has %d items, want 7", len(full.Items))
	}
	want := issueIDs(full.Items)
	if full.Items[0].ReadyRank == nil || *full.Items[0].ReadyRank != 5 {
		t.Fatalf("first item rank = %v, want 5", full.Items[0].ReadyRank)
	}
	if full.Items[2].ReadyRank != nil {
		t.Fatalf("third item rank = %v, want the unranked bucket", *full.Items[2].ReadyRank)
	}

	var traversed []string
	var cursor string
	for {
		page, err := service.ListIssues(ctx, domain.ListIssuesInput{Limit: 2, Cursor: cursor})
		if err != nil {
			t.Fatalf("cursor %q: %v", cursor, err)
		}
		traversed = append(traversed, issueIDs(page.Items)...)
		if !page.HasMore {
			break
		}
		cursor = *page.NextCursor
	}
	if !reflect.DeepEqual(traversed, want) {
		t.Fatalf("cursor traversal = %v, want %v", traversed, want)
	}
}

func TestUpdateIssueReadyRankSetsAndClearsPosition(t *testing.T) {
	service, _, _ := openIssueService(t)
	ctx := context.Background()
	created, err := service.CreateIssue(ctx, domain.CreateIssueInput{
		Type: domain.TypeTask, Title: "queued", Status: domain.StatusReady,
	})
	if err != nil {
		t.Fatal(err)
	}

	rank := int64(4)
	updated, err := service.UpdateIssue(ctx, domain.UpdateIssueInput{
		IssueID: created.ID, ExpectedVersion: created.Issue.Version,
		Changes: domain.IssuePatch{ReadyRank: domain.OptionalInt64{Set: true, Value: &rank}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Issue.ReadyRank == nil || *updated.Issue.ReadyRank != 4 || !containsString(updated.ChangedFields, "ready_rank") {
		t.Fatalf("set rank = %#v %v", updated.Issue.ReadyRank, updated.ChangedFields)
	}
	reloaded, err := service.GetIssueProjection(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.ReadyRank == nil || *reloaded.ReadyRank != 4 {
		t.Fatalf("reloaded rank = %v, want 4", reloaded.ReadyRank)
	}

	cleared, err := service.UpdateIssue(ctx, domain.UpdateIssueInput{
		IssueID: created.ID, ExpectedVersion: updated.Issue.Version,
		Changes: domain.IssuePatch{ReadyRank: domain.OptionalInt64{Set: true, Value: nil}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if cleared.Issue.ReadyRank != nil || !containsString(cleared.ChangedFields, "ready_rank") {
		t.Fatalf("cleared rank = %#v %v", cleared.Issue.ReadyRank, cleared.ChangedFields)
	}
	reloaded, err = service.GetIssueProjection(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.ReadyRank != nil {
		t.Fatalf("reloaded cleared rank = %v, want nil", *reloaded.ReadyRank)
	}
}

func findIssue(t *testing.T, items []domain.IssueProjection, id string) domain.IssueProjection {
	t.Helper()
	for _, item := range items {
		if item.ID == id {
			return item
		}
	}
	t.Fatalf("issue %s not in page", id)
	return domain.IssueProjection{}
}

func indexOfIssue(items []domain.IssueProjection, id string) int {
	for index, item := range items {
		if item.ID == id {
			return index
		}
	}
	return -1
}

func issueIDs(items []domain.IssueProjection) []string {
	ids := make([]string, len(items))
	for index, item := range items {
		ids[index] = item.ID
	}
	return ids
}
