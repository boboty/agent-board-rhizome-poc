# Agent Board

Agent Board is an open-source shared task board plus AI engineering workflow standard for software development with multiple agents, harnesses, and worktrees.

## Why it exists

AI-assisted development is increasingly split across daily coding tools such as Codex, Claude Code, and OpenCode, and orchestration tools such as Paseo and Orca. The execution tools are different, but the work still needs one shared place to answer:

- What work is ready?
- Who or what is working on it?
- Which worktree and commit represent the current delivery?
- Is the task waiting for independent verification?
- Was it returned for rework?
- Does it need a human decision?
- Is it actually done?

Agent Board provides that shared state without trying to replace IDEs, coding agents, harnesses, Git, or project-management suites.

## Product shape

Agent Board has two inseparable parts:

1. **AI Engineering Workflow Standard** — how tasks are defined, executed, verified, returned for correction, handed off, escalated, and closed.
2. **Shared Task Board** — the shared runtime view used by people and agents to create, schedule, claim, advance, verify, and close work.

The standard defines the workflow. The board makes that workflow operable across multiple agents, harnesses, and worktrees.

## Core principles

- **Orchestrator is the sole owner of task-level lifecycle transitions** (READY → IN PROGRESS → DONE / BLOCKED). Developer and Verifier execute work and return evidence (PASS / RC / BLOCKED + evidence), but never directly advance the task-level board state.
- Keep developers in their existing tools. Codex, Claude Code, OpenCode, Paseo, Orca, and similar tools can create tasks, execute work, and report results to the Orchestrator. The Orchestrator drives the task-level state.
- The board is the shared task state, not an agent runtime. Agent execution remains the responsibility of the harness or coding tool.
- Tasks may be created cheaply, but execution begins only when the task is ready to be worked and verified.
- Developer completion is not final completion. Independent verification is required before DONE.
- RC means verifier rejection followed by developer correction and a new independent verification round. Throughout this cycle the task-level state stays IN PROGRESS — only PASS moves to DONE, and only an explicit BLOCKED stops the task.
- Human intervention should be explicit and limited to decisions that require authority or judgment.
- Do not grow into Jira. V0.1 has a READY queue, not sprint planning, estimates, deadlines, or portfolio management.

## Relationship to rhizome-mcp

Agent Board is forked from rhizome-mcp because rhizome already provides hard shared-state primitives that should not be rewritten lightly: claims, leases, resumable attempts, checkpoints, review requests, stale-review protection, gates, SQLite persistence, MCP, and a local board service.

Agent Board will extend that foundation rather than replace it.

The primary additions are human-facing workflow semantics and a writable shared board suitable for the workflow described above.
