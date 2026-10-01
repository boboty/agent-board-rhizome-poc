# AI Engineering Workflow Standard

This directory will contain the workflow standard used together with Agent Board.

The first version is intentionally based on practices already proven in real projects rather than a new theoretical agent-governance model.

## Planned structure

- `00-overview.md` — scope, principles, terminology
- `01-task-definition.md` — task goal, scope, constraints, acceptance criteria
- `02-orchestration.md` — Orchestrator responsibilities and boundaries
- `03-development.md` — Developer execution and self-check
- `04-verification.md` — Independent Verifier and PASS rules
- `05-rc-and-handoff.md` — correction loop, interruption, takeover, continuation
- `06-human-decision.md` — stop-for-decision and authoritative decisions
- `07-task-board.md` — shared board state and workflow transitions

## Baseline workflow

Task defines the work. Agent Board holds shared workflow state. Workspace/Git holds the delivery. Agent activity holds execution detail. Independent Verifier supplies completion evidence.

**Orchestrator is the sole owner of task-level lifecycle transitions.** Developer and Verifier execute work and return evidence (PASS / RC / BLOCKED + evidence) to the Orchestrator; they never directly advance the board-level state (READY / IN PROGRESS / DONE / BLOCKED).

Normal flow:

```text
READY
  -> Orchestrator starts task                    (task state: IN PROGRESS)
  -> Developer execution                         (task state: IN PROGRESS)
  -> verification                                (task state: IN PROGRESS)
      -> PASS -> DONE                            (task state: DONE)
      -> RC   -> Developer correction -> new verification
                                                  (task state: IN PROGRESS —
                                                   execution has started,
                                                   so the task stays
                                                   IN PROGRESS across the RC
                                                   round; only an explicit
                                                   Orchestrator BLOCKED
                                                   could stop it)
      -> DECISION REQUIRED -> human decision -> resume or close
                                                  (task state: BLOCKED)
```

The Agent Board shows the four task-level states in parentheses. Verification,
RC, and decision phases are execution detail inside a task, not board columns.

The standard remains independent of any specific coding agent, harness, model provider, or effort level.
