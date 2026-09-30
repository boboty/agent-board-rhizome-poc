# Status board contract

This document is the canonical contract for the shipped status board in Rhizome MCP.

## 1. Scope

The status board is a loopback-only, single-user local status display. It
surfaces live project state including active work attempts (leases), blocked
issues and their reasons, pending review requests, and a bounded planning graph
for dependency inspection. The served board adds a deliberately minimal write
surface — create a task, edit a task, queue it into READY, and reorder READY —
described in §11; everything else stays read-only.

The board is not a hosted service, does not support authentication, multi-user access, or remote deployment, and accepts no writes other than the four task actions in §11 — all of them same-origin, CSRF-tokened, and served only to a loopback browser. It is designed for a single developer running locally to inspect project state during concurrent agent work.

## 2. Output surfaces

The status board data is surfaced through four independent rendering paths, each with its own compatibility guarantee:

- **CLI table** (`rhizome-mcp board`): ASCII table with status counts, active leases, blocked issues, and the review queue. Format is human-friendly and intentionally compact. Existing column names are stable, but columns may be added, so consumers should key off the header row rather than field positions — see §2.1 for the executor columns added to the active-attempts table.
- **CLI JSON** (`rhizome-mcp board --format json`): Complete board data as structured JSON. The JSON schema is authoritative; table rendering applies domain-specific summarization and does not carry the full scope of the JSON shape. Future schema evolution is additive; clients must tolerate unknown fields.
- **Static HTML snapshot** (`rhizome-mcp board --output PATH`): A self-contained, embeddable HTML file with inline CSS and all data needed for rendering. The snapshot is a discrete artifact; snapshots from different times are independent and do not communicate with any server. The snapshot is suitable for archival, diff, sharing, or embedding in CI reports.
- **Served board** (`rhizome-mcp board --serve`): An independent HTTP process listening at a loopback endpoint, with JSON API routes and an interactive HTML page. The served process lives outside any MCP session or command context and terminates on interrupt.

Each surface independently renders the same board result. None carry backwards compatibility burden for the others; a change to the table format is independent of the JSON schema, which is independent of the HTML rendering.

### 2.1. Active-attempt executor metadata

Every surface renders the same runtime information for one active attempt, so a
consumer can answer "who or what is running this, and where" without a second
read:

| Field | JSON key | CLI table column | HTML column |
| --- | --- | --- | --- |
| Agent label | `session_label` | `session_label` | Session label |
| Stable execution instance | `session_instance_key` | `session_instance_key` | Instance key |
| Harness / client | `session_client_name` | `session_client_name` | Client |
| Model | `session_model` | `session_model` | Model |
| Worktree | `session_worktree` | `session_worktree` | Worktree |
| Lease expiry | `lease_expires_at` | `lease_expires_at` | Lease expires |
| Session record | `session_id` | (not rendered) | (not rendered) |

The values come from a read-time join between the leased `work_attempts` row
and its `agent_sessions` row (`docs/02` §4), the same session metadata
`create_agent_session` records. `session_instance_key` is the advisory stable
execution-instance key described in `docs/02` §4.1, not an enforced identity.

Degradation is part of the contract: an attempt claimed without an
`agent_session_handle`, or a session that reported only part of its metadata,
stays on the board with the missing fields absent rather than erroring or
hiding the attempt. The JSON projection omits an absent field; the CLI table
leaves the cell empty; both HTML views render the em-dash placeholder. An
attempt is only listed while its lease is unexpired and its status is `active`,
so a finished or expired attempt is never shown as the current executor.

The CLI table is a human-facing TSV with a header row rather than a stable
column contract: adding the executor columns moved `lease_expires_at` from the
fifth field to the ninth. Positional consumers of `rhizome-mcp board` output
should switch to the JSON surface (`--format json`), whose schema is the
authoritative one and evolves additively.

Because these fields are part of the response content, they participate in the
`/api/board` semantic ETag (§4): a change to an executor's instance key, model,
or worktree changes the ETag and therefore reaches a polling board.

## 3. Routes and response shapes

All GET/HEAD routes below are read-only. The four POST routes in §11 are the
board's only write surface; any other method returns 405 Method Not Allowed
with `Allow: GET, HEAD, POST`.

- **`GET /`** — HTML board page (interactive, same-origin fetch for updates via API routes)
- **`GET /search`** — HTML search page (issue search and snippet results)
- **`GET /issues/{id}`** — HTML issue-detail page (including attempts, activities, notes, and related issues)
- **`GET /api/board`** — JSON board data (supports ETag / If-None-Match for conditional refresh)
- **`GET /api/search`** — JSON search results. Query parameters: `q` (required; omitting it returns 400), `entity_type`, `limit`, `cursor`, `snippet_length`, `include_archived`. Any other query parameter is rejected.
- **`GET /api/issues/{id}`** — JSON issue detail (supports ETag / If-None-Match). The bare `/api/issues` path reaches the same handler but has no issue to resolve and returns 404.
- Anything else — 404 Not Found

If the underlying board service is unavailable, all routes return 503 Service Unavailable with body `service unavailable`.

## 4. ETag and If-None-Match semantics

ETags on `/api/board` and `/api/issues/{id}` are semantic, derived from the response content itself, not from timestamps or version counters. An unchanged board therefore yields byte-identical ETags and content.

A conditional request with an `If-None-Match` header matching the current ETag returns `304 Not Modified` with no body. This makes polling for board updates safe: if content has not changed, the response carries no data and the client's parse burden is zero.

## 5. HTTP method, Host, Origin and CSP posture

### Method enforcement

All routes accept GET and HEAD only. HEAD requests follow HTTP semantics: response headers are identical to GET, but no body is sent. POST, PUT, DELETE, and all other methods return 405 with the `Allow: GET, HEAD` header.

### Host and Origin validation

The server enforces the local trust boundary as described in [docs/08-local-http-transport.md](docs/08-local-http-transport.md), using the same validation rules as the MCP HTTP transport:

- A missing or unparseable `Host` header returns 400 Bad Request. A bare hostname with no port is unparseable in this sense, so `Host: example.com` returns 400.
- A well-formed `Host` authority that does not match the bound loopback authority returns 421 Misdirected Request — both a wrong port (`127.0.0.1:1`) and a wrong host (`example.com:<bound port>`).
- If an `Origin` header is present and does not exactly equal `scheme://<bound authority>`, the request is rejected with 403 Forbidden.
- If no `Origin` header is present (typical for browser navigation and non-browser MCP clients), the request is allowed.

Forwarded headers (`Forwarded`, `X-Forwarded-*`) are not trusted.

### Content Security Policy

Every response carries:

```
Content-Security-Policy: default-src 'none'; style-src 'unsafe-inline'; img-src 'self' data:; script-src 'unsafe-inline'; font-src 'self' data:; connect-src 'self'; base-uri 'none'; form-action 'self'
```

This policy prevents all external loads and permits form submission only back to this origin (`form-action 'self'`), which is what the §11 write forms use; it was `'none'` while the board was read-only. Inline styles and scripts are permitted for the interactive board and search page. Images and fonts must be served by the same origin or inlined as data URIs.

### Other security headers

Every response also sets:

- `X-Content-Type-Options: nosniff` — Prevents MIME type sniffing.
- `Referrer-Policy: no-referrer` — Omits referrer information on all requests.
- `Cache-Control: no-store` — Prevents caching by browsers and intermediaries.

## 6. Bounded collections and truncation reporting

The board bounds four collections so responses stay predictable in size:

- **Blocked issues** — at most 100 entries.
- **Active attempts** — at most 100 entries.
- **Active reservations** — at most 100 entries.
- **Review requests** — at most 100 entries.

Each carries a boolean flag in the response's `truncation` object, surfaced in
the JSON response, the CLI table (as a `truncated` marker row), both HTML views
(as a text note below the table), and the semantic ETag. Each flag means "first
`MaxBoardCollectionLimit` shown; more exist", not a total. The flag is set when
the query that loaded the collection returned more results than the limit.

`attempt_gates` is one row per active attempt, so it shares `active_attempts`'s
flag. The workflow projection (§8) adds its own bounded reads and reports them
under `workflow.truncation` rather than the four flags above.

**Active reservations pre-filter semantics:** the `truncation.active_reservations`
flag is set from the reservation page's result *before* `filterReservationsByActiveAttempts`
runs. This is deliberate, to detect when the raw query was truncated. Consequently,
a truncated pre-filter reservation list may show fewer than 100 entries after
filtering if some reservations belong to attempts that are not currently active.
Orphaned reservation rows awaiting expiry sweep can also push a live reservation
out of the visible window.

The planning graph is bounded separately, by a node budget (default 100 nodes,
maximum 500), and it *does* report its cut: the response carries `truncated`
and a single-valued `truncation_reason` whenever a *budget* dropped something a
larger budget would have kept — `"node_limit"` for the node budget,
`"entry_point_limit"` for the entry-point cap, with `node_limit` taking
precedence when both apply (which in practice it always does, since every entry
point is also a traversal root). Deterministic selection outcomes are not
truncation and set neither field: a terminal issue dropped because no relation
edge attaches it to retained work, or excluded outright by
`include_terminal: false`.

Two properties of the graph's node selection matter to a board consumer:

- Non-terminal work — anything not `done` or `cancelled` — claims the node
  budget first. Terminal work is admitted afterwards, and only where a relation
  edge attaches it to a retained node. The board additionally requests the graph
  with terminal issues excluded outright, so finished work cannot consume the
  budget at all.
- `summary.entry_point_count` is computed over every claimable issue in the
  snapshot, not over the returned `nodes`, so truncation can never shrink how
  much claimable work a client is told exists. The consequence is that
  `entry_points` may name an issue that does not appear in `nodes` — under the
  node budget, and equally when an issue lies outside the traversal depth.
- The serialized `entry_points` list is capped at the same node budget, keeping
  the board's JSON, table and ETag payloads bounded on a project with thousands
  of claimable issues. `entry_point_count` greater than `len(entry_points)`,
  together with the truncation reason, is how a consumer sees that the list was
  capped; the kept prefix follows the deterministic snapshot order, so a live
  board poll does not see the window shuffle.

## 7. Workflow gate visibility

The board surfaces workflow-gate state (docs/02 §17) read-only, reusing the
same compact summary `get_work_context` reports so a human reading the board
and an agent reading context see identical state (ISSUE-175).

- **Board pages and JSON** carry one gate-progress row per active attempt
  (`attempt_gates`, joined to `active_attempts` by `attempt_id`): the
  enforcement point the attempt holder will hit, the frozen snapshot
  fingerprint when one supplied the requirements, requirement/satisfied
  counts, and each unmet requirement's key and reason. The collection shares
  the active-attempts bound. The HTML attempt table renders this as a
  `Gates` column — progress as text (`1/2 satisfied`, or `none apply`), with
  unmet requirement keys listed beneath. Issues without an active attempt
  are not evaluated on the board; their summary is on their issue-detail
  page.
- **Issue-detail pages and JSON** always carry the issue's full summary
  (`Gates`): the evaluated enforcement point (an active attempt evaluates
  its frozen snapshot at `complete_work_to_done`, otherwise live policies at
  `claim_work`), progress counts, and one row per unmet requirement with its
  reason and the imperative next action that clears it. A project with no
  matching policies reports `requirement_count` 0 and the page states that
  no gate requirements apply — the no-policy compatibility case.

Gate state participates in the semantic ETags of §4, so a polling client
refreshes when a requirement is satisfied even if nothing else changed. The
served board never mutates gate state from the browser: it is displayed, and
the §11 write surface has no gate action.

## 8. Workflow (Kanban) projection

The board renders a human workflow view of the same project state: four
task-level columns, one card per issue. It is a projection, not a second status
store, and it is not a process-phase view.

- **READY** — stored `ready` and no active attempt: the task can be executed
  now.
- **IN PROGRESS** — the task has an active attempt of any kind, or it is stored
  `review` (delivered, awaiting or undergoing verification). Development,
  independent verification, changes-requested rework, and re-verification are
  phases of this one task-level state.
- **DONE** — stored `done`.
- **BLOCKED** — stored `blocked`. The cause (external dependency, workflow
  gate, human decision, orchestration dead end) is card detail; the board never
  splits it into separate columns, and it never guesses which kind it was.

There is deliberately no VERIFYING, RC, or DECISION REQUIRED column. Those were
process phases, and AB-5 collapsed them into their task-level states: an active
verifier is IN PROGRESS, a changes-requested round is READY work again (stored
`ready` after the failed round, so it is claimable) with the round as card
detail, and a blocked review is BLOCKED with the review outcome as card detail.

### 8.1. Derivation rules

Each issue is placed by the first matching rule, and the derivation reads only
the stored status and the active attempt — no review, verification, or
changes-requested signal can move a card between columns:

1. archived → not projected;
2. `done` → DONE;
3. `cancelled` → not projected;
4. an active attempt of any kind → IN PROGRESS (checked before the stored
   status, because a claimed issue keeps its stored status while its effective
   status is derived);
5. `review` → IN PROGRESS;
6. `blocked` → BLOCKED;
7. `ready` → READY;
8. anything else → not projected.

### 8.2. One task, one column

Exactly one card is produced per issue, so an issue can never occupy two
columns. An issue that no column honestly describes is not guessed into one:
it is reported in the projection's unprojected list with a machine-readable
reason (`archived`, `cancelled`, `not_ready`, `unknown_status`) and a human
sentence. A consumer therefore sees that work was left out and why. Stored
`open` tasks are reported this way, and the served board offers the §11 queue
action on them.

### 8.3. Card fields and degradation

A card carries the task identifier and title, type, priority, `is_claimable`,
and, when they exist: the READY-queue rank (rendered on READY cards, the only
column whose position it orders), the blocked reason (rendered on BLOCKED
cards), the active attempt's kind plus the claiming session's executor
attribution (label, instance key, client, model, worktree, lease expiry),
the review request's identifier, status, target version, and timestamps with
the count of `changes_requested` rounds read, and the issue's
commit/branch/pull-request artifacts as delivery references.

Every optional field is omitted from the JSON when the underlying data did not
carry it; the HTML views render an em-dash placeholder rather than an empty
cell. The board never synthesizes a developer, verifier, or commit it did not
read: an attempt claimed without a session handle leaves the executor fields
absent, and an issue with no commit artifact has no delivery reference.

Review, verification, and changes-requested information therefore stays fully
readable — `review_status`, `review_target_version`, `changes_requested_count`,
the review timestamps, `attempt_kind`, and the executor fields — as card detail
and in the review-request auxiliary section, without becoming a task-level
state.

Several bounded reads can leave a card's information incomplete, and each is
reported rather than hidden under `workflow.truncation`: `ready`, `review`,
`blocked`, and `done` cover the four stored-status issue reads,
`unprojected` covers the open/cancelled read, and `review_requests` marks that
a card's review state may be older than the newest request. A card shows at
most `boardDeliveryReferenceLimit` delivery references; `delivery_overflow`
marks that some card shows fewer references than its issue has (the per-card
cap or a truncated artifact read), and `delivery_unavailable` marks that a
card's delivery references could not be read at all. A card count is therefore
always a lower-bound-safe number whenever its flag is set.

### 8.4. Surfaces

- **CLI JSON** (`rhizome-mcp board --format json`) and **`GET /api/board`**
  carry a `workflow` object with `columns`, `cards`, `unprojected`, and
  `truncation`. The addition is additive: every pre-existing field keeps its
  meaning.
- **CLI table** carries `workflow`, `workflow_cards`, and
  `workflow_unprojected` sections after `status_counts`.
- **HTML** renders the Kanban as the page's primary view on both the static
  snapshot and the served board. The status counts, active attempts, blocked
  issues, review queue, and planning graph remain as auxiliary sections.

The projection participates in the semantic ETag (§4), so a card moving
between columns reaches a polling client even when nothing else changed. The
served board never drags cards: column changes happen only through the §11
task actions (queue into READY, move up/down within READY).

An explicit verification-round ordinal is not projected. The bounded review
read can count how many `changes_requested` requests an issue has, which is
what the card shows, but the total number of verification rounds an issue has
been through is not addressable without a per-issue review history read, and
the free text a reviewer wrote when requesting changes lives on the review
outcome record, which the board's bounded read does not carry.

## 9. Process lifetime and stdout contract

The served board is an independent process started by `rhizome-mcp board --serve` and is unrelated to any MCP session context. It holds no session state and terminates cleanly on interrupt (SIGINT/SIGTERM).

On successful listen, exactly one line is written to stdout:

```
http://HOST:PORT/
```

(with trailing slash). The VS Code extension parses this line to extract the server endpoint. Treat it as a stable contract: the format must not change.

## 10. Scope exclusions

The status board explicitly does not include:

- Write operations beyond the minimal task loop in §11. There is no browser
  action for claim/lease, RC, verifier PASS/reject, human decisions, agent
  startup, or arbitrary tool forwarding, and no drag-and-drop: READY order is
  changed only by the explicit move-up/move-down action, which writes
  `ready_rank` through the ordinary issue update path. The board also does not
  enforce an independent verifier, track an RC owner or preferred developer, or
  model handoff: those remain workflow capabilities, not board states.
- Authentication or user accounts. It is local-only and has no credential or permission model.
- Remote or multi-user hosting. It is not designed for deployment on the internet or behind a reverse proxy.
- Cross-origin requests. CORS is not implemented; only same-origin requests (or requests with no Origin header) are accepted.

## 11. Minimal task write API

The served board exposes exactly four task actions and nothing else: create a
task, edit its text fields, queue it into READY, and move a READY card one
position. Every route calls an existing application use case
(`BoardCommandService`, which composes `IssueService`); no SQL is issued from
the HTTP layer, no rule is duplicated, and there is no generic MCP/CLI
forwarding route.

### 11.1. Routes

| Method | Path | Purpose |
| --- | --- | --- |
| `POST` | `/api/issues` | Create a task (`title`, `description`, `acceptance_criteria`, `priority`, `status` = open\|ready, optional `ready_rank`). |
| `POST` | `/api/issues/{id}` | Edit `title`, `description`, `acceptance_criteria`, `priority`; requires `expected_version`. |
| `POST` | `/api/issues/{id}/ready` | Queue an **open** task: stored status becomes `ready`, with optional `ready_rank`. Other statuses are refused, because `done -> ready` is a legal transition and the queue button must not become a reopen backdoor. |
| `POST` | `/api/issues/{id}/rank` | Move a READY card `up` or `down`; requires `expected_version`. |

`{id}` accepts a display ID (`ISSUE-N`) or an internal ULID, like every other
issue command. Requests are `application/x-www-form-urlencoded`; anything else
is 415.

Two response shapes, chosen by `Accept`:

- **Browser form** (no `application/json`): `303 See Other` to
  `/` (or to a submitted `return` path, restricted to `/` and
  `/issues/{id}`) with `?notice=created|updated|queued|reordered`, or
  `?error=version_conflict|invalid_input|not_found|failed`. The redirect makes
  the browser reload current data (POST/redirect/GET), so a conflict always
  shows freshly read state instead of a stale form.
- **JSON client** (`Accept: application/json`): the persisted issue
  (`201`/`200`) or `{code, message}` with `409` for a version conflict, `400`
  for invalid input, `404` for a missing issue.

### 11.2. CSRF and same-origin

A write is accepted only when **all** of these hold:

- the served process generated its per-process synchronizer token (otherwise
  writes return 503 rather than running unprotected);
- the request carries that token, either as the `csrf_token` form field or the
  `X-CSRF-Token` header, compared in constant time;
- `Origin`, when present, equals `http(s)://<request Host>`;
- `Sec-Fetch-Site`, when present, is `same-origin` or `none`;
- the transport-level Host check in `docs/08` still passes, so a write to a
  non-loopback authority never reaches the handler.

Write forms are rendered on `/` (create, READY moves, queue) and on
`/issues/{id}` (edit, queue); `/search` renders none, because it is a read view.

The token is embedded in every served write form and never written to a cookie,
so a cross-site page cannot read it; a cross-site form post therefore fails the
token check even in browsers that omit `Origin`. A refused write changes
nothing and is reported as 403.

The Content-Security-Policy is `form-action 'self'` (was `'none'` before this
surface existed) so the board's own forms may post to itself. The offline
snapshot (`board --output`) renders no write controls at all: it is a file, not
a server.

### 11.3. READY ordering

`ready_rank` (AB-1) remains the only ordering mechanism; no queue table and no
second status store are introduced. The board displays the READY column with
`domain.CompareReadyQueue`: explicit `ready_rank` ascending first, unranked
entries after all ranked ones, then priority, then display ID.

Moving a card recomputes that displayed order and renumbers the bounded READY
column in steps of 10, writing only the issues whose rank changed. The plan is
built from the *projected* READY column (`BoardService.ReadyQueue`), not from
stored status: a stored-`ready` issue that the projection displays as IN
PROGRESS (it has an active attempt) is not in the queue and is never rewritten,
so a task that is actually being worked on never moves in the queue. Because
the plan uses the same
comparator the column is rendered with, what the operator saw is what is
reordered. Only stored-`ready` issues are read or written, so reordering cannot
alter any other status's ordering, and a READY queue larger than the board's
collection limit is refused rather than partially reordered, and so is a
board whose active-attempt read was cut, because a missing attempt would
hide a card that belongs in IN PROGRESS.

A card that is already first or last is a successful no-op. Every write carries
the version read in the same request, and the moved card must match the
`expected_version` the form submitted, so a concurrent edit is reported as a
version conflict (`409` to a JSON client, `?error=version_conflict` to a
browser) rather than overwritten. A conflict raised for a neighbour after an
earlier write in the same reorder leaves the queue in a consistent, partly
renumbered order; the operator refreshes and retries, and no version is ever
silently overwritten.
