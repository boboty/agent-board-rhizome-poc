package domain

import "strings"

// ReadyQueueEntry is the ordering input for one stored-ready issue's position
// in the READY queue. It is deliberately small: the queue order is derived
// from the issue's explicit ready_rank (AB-1), then priority, then its display
// identifier, so both the board's READY column and any reorder operation use
// exactly one comparator and can never disagree about what "before" means.
type ReadyQueueEntry struct {
	DisplayID string
	ReadyRank *int64
	Priority  Priority
}

// CompareReadyQueue orders two READY-queue entries exactly the way the board's
// READY column is displayed: an explicit lower ready_rank first, unranked
// entries after every ranked one, then higher priority first, then display
// identifier ascending as the deterministic final tie-break. It returns a
// negative number when a sorts before b.
//
// The comparator is pure and total, so the queue order is stable across reads
// and a reorder can be planned from it without touching storage.
func CompareReadyQueue(a, b ReadyQueueEntry) int {
	switch {
	case a.ReadyRank == nil && b.ReadyRank != nil:
		return 1
	case a.ReadyRank != nil && b.ReadyRank == nil:
		return -1
	case a.ReadyRank != nil && b.ReadyRank != nil && *a.ReadyRank != *b.ReadyRank:
		if *a.ReadyRank < *b.ReadyRank {
			return -1
		}
		return 1
	}
	if left, right := priorityOrder(a.Priority), priorityOrder(b.Priority); left != right {
		if left < right {
			return -1
		}
		return 1
	}
	return strings.Compare(strings.TrimSpace(a.DisplayID), strings.TrimSpace(b.DisplayID))
}

// compareReadyCards orders two cards already known to be in the READY column.
func compareReadyCards(a, b BoardWorkflowCard) int {
	return CompareReadyQueue(
		ReadyQueueEntry{DisplayID: a.IssueDisplayID, ReadyRank: a.ReadyRank, Priority: a.Priority},
		ReadyQueueEntry{DisplayID: b.IssueDisplayID, ReadyRank: b.ReadyRank, Priority: b.Priority},
	)
}
