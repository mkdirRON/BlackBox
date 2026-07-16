# BlackBox — Architecture & Contributor Guide

This document explains **how BlackBox works, end to end** — the ideas, the data,
the code, and the reasoning behind each decision. If you have never seen this
project before, read it top to bottom and you should be able to fix a bug or add
a feature with confidence.

> New here? Read sections 1–5 for the mental model, then jump to the package that
> owns whatever you want to change (section 8), then read "How to make common
> changes" (section 13).

---

## Table of contents

1. [What BlackBox is](#1-what-blackbox-is)
2. [The problem it solves](#2-the-problem-it-solves)
3. [Core vocabulary](#3-core-vocabulary)
4. [The mental model in one picture](#4-the-mental-model-in-one-picture)
5. [End-to-end lifecycle](#5-end-to-end-lifecycle)
6. [Repository layout](#6-repository-layout)
7. [The data model](#7-the-data-model)
8. [Package-by-package deep dive](#8-package-by-package-deep-dive)
9. [The capture mechanism, in depth](#9-the-capture-mechanism-in-depth)
10. [Command walkthroughs](#10-command-walkthroughs)
11. [Claude Code hooks integration](#11-claude-code-hooks-integration)
12. [Build, run, and test](#12-build-run-and-test)
13. [How to make common changes](#13-how-to-make-common-changes)
14. [Design decisions & FAQ](#14-design-decisions--faq)
15. [Known limitations & edge cases](#15-known-limitations--edge-cases)
16. [Glossary](#16-glossary)

---

## 1. What BlackBox is

BlackBox is a **single Go binary** — a command-line tool — that records what an
AI coding agent (specifically [Claude Code](https://docs.claude.com/en/docs/claude-code))
did to your repository, **one prompt at a time**. For every prompt you send, it
stores the prompt text together with the *exact* file changes that prompt
produced.

Once recorded, you can:

- **`log` / `show`** — browse the session as a timeline of prompts and diffs.
- **`serve`** — view that timeline in a local web page.
- **`revert`** — undo the changes of a single prompt (not the whole session).
- **`blame`** — ask "which prompt last changed this line?"

Everything is stored locally in SQLite at `~/.blackbox/sessions.db`. Nothing is
sent anywhere.

---

## 2. The problem it solves

Git records the *final state* of your code, but not the reasoning chain that got
you there. After a burst of AI prompts you end up with a pile of changes and no
easy way to answer:

- What did *this specific prompt* change?
- Which prompt introduced this bug?
- Can I undo just prompt #7 without unwinding prompts #8–#12?

A single commit usually bundles the work of many prompts, so `git blame` points
at "the AI session," not the instruction that changed a line. BlackBox adds a
**prompt-level ledger** on top of git so those questions have answers.

---

## 3. Core vocabulary

These four words appear everywhere. Learn them first.

| Term | Meaning |
| --- | --- |
| **Session** | One Claude Code session, identified by the `session_id` Claude assigns. Scoped to one repo. Think "one sitting." |
| **Turn** | One prompt within a session, **plus** the complete set of file changes the agent made while answering it. This is the unit BlackBox is built around. |
| **Diff** | One file's worth of change within a turn, stored as a real unified patch (the kind `git apply` understands). A turn has zero or more diffs. |
| **Baseline tree** | A git *tree object* (a content snapshot of the whole working directory) taken the moment a prompt is submitted. Comparing it to a later snapshot yields the turn's diffs. |

Relationship: **a session has many turns; a turn has many diffs.**

```
session ──< turn ──< diff
```

---

## 4. The mental model in one picture

A turn is bracketed by two events. When the prompt is **submitted** we take a
"before" snapshot. When the agent **stops** we take an "after" snapshot. The
difference between them *is* what the prompt did.

```
   prompt submitted                          agent stops
        │                                         │
        ▼                                         ▼
   ┌─────────┐      agent edits files       ┌─────────┐
   │ snapshot│  ─────────────────────────►  │ snapshot│
   │  BEFORE │                              │  AFTER  │
   └─────────┘                              └─────────┘
        └──────────────── diff ──────────────────┘
                    = this turn's changes
```

Concrete example. Suppose across one session you send two prompts:

```
#1  "add a greeting to main"       → changes main.go   (+3 -1)
#2  "add a cache file for lookups" → creates cache.go  (+3 -0)
```

BlackBox stores turn #1 with a diff for `main.go`, and turn #2 with a diff for
`cache.go`. It knows they are *separate* because each turn has its own before/after
snapshot. `blackbox revert <turn-2>` deletes `cache.go` and leaves `main.go`
alone.

---

## 5. End-to-end lifecycle

Here is the whole system in one sequence. The only "magic" is that Claude Code
calls our binary at two moments via its hook system (see [section 11](#11-claude-code-hooks-integration)).

```
 ┌────────────┐
 │ Developer  │ runs `blackbox init` once in the repo
 └─────┬──────┘
       │  writes UserPromptSubmit + Stop hooks into .claude/settings.json
       ▼
 (later, while coding with Claude Code)

 Developer submits a prompt
       │
       ▼
 Claude Code runs the UserPromptSubmit hook:  blackbox hook  (JSON on stdin)
       │        ├─ InsertSession(session_id, repo)          [idempotent]
       │        ├─ SnapshotTree(repo)  ──► baseline tree T0
       │        └─ InsertTurn(prompt, baseline = T0)         [turn is now OPEN]
       ▼
 Claude edits/creates files (its Write/Edit tools change the working tree)
       │
       ▼
 Claude finishes responding
       │
       ▼
 Claude Code runs the Stop hook:  blackbox hook  (JSON on stdin)
                ├─ OpenTurn(session_id) ──► the still-open turn from above
                ├─ SnapshotTree(repo)  ──► final tree T1
                ├─ DiffTrees(T0, T1)   ──► one FileDiff per changed file
                ├─ InsertDiff(...)     ──► one row per file
                └─ FinalizeTurn        [turn is now CLOSED]

 (any time afterwards)

 Developer runs blackbox log / show / serve / revert / blame / status
       │
       ▼
 Reads the SQLite DB (and, for revert, calls `git apply --reverse`).
```

The key insight: **hooks are separate, short-lived processes with no shared
memory.** The "turn is open" state that connects the two hook invocations lives
in the database (a turn row whose `ended_at` is still NULL), not in RAM.

---

## 6. Repository layout

```
BlackBox/
├── go.mod, go.sum          module = github.com/mkdirRON/BlackBox ; Go 1.25+
├── README.md               user-facing docs
├── ARCHITECTURE.md         this file
├── cmd/
│   └── BlackBox/
│       ├── main.go         CLI entry point: arg dispatch + the `hook` runner
│       └── commands.go     every subcommand's implementation + shared helpers
└── internal/
    ├── capture/            turns hook events (JSON) into recorded turns + diffs
    │   └── capture.go
    ├── db/                 SQLite schema, connection, and all queries
    │   └── db.go
    ├── git/                working-tree snapshots, tree diffing, reverse-apply
    │   ├── git.go
    │   └── git_test.go
    ├── hooks/              merge-installs hooks into .claude/settings.json
    │   ├── hooks.go
    │   └── hooks_test.go
    └── ui/                 the `serve` web timeline (net/http + html/template)
        └── ui.go
```

**Dependency direction** (who imports whom). Arrows point "depends on." There are
no cycles.

```
cmd/BlackBox ──► internal/capture ──► internal/db
             │                    └─► internal/git
             ├─► internal/hooks
             ├─► internal/ui ─────────► internal/db
             ├─► internal/db
             └─► internal/git
```

`internal/db` and `internal/git` are the two leaf packages — they depend only on
the standard library (plus the SQLite driver). Everything else builds on them.

---

## 7. The data model

One SQLite file, three tables. The schema string lives in `internal/db/db.go`
(the `schema` constant).

### Tables

**`sessions`** — one row per Claude Code session.

| Column | Type | Notes |
| --- | --- | --- |
| `session_id` | TEXT PK | The id Claude Code assigns (UUID-like). |
| `repo_path` | TEXT | Working directory the session ran in. |
| `started_at` | INTEGER | Unix seconds, set on first insert. |

**`turns`** — one row per prompt.

| Column | Type | Notes |
| --- | --- | --- |
| `turn_id` | TEXT PK | Random 16-hex-char id (`newID()` in capture). |
| `session_id` | TEXT FK → sessions | Which session this prompt belongs to. |
| `prompt` | TEXT | The prompt text. |
| `turn_number` | INTEGER | 1-based position within the session. |
| `time_prompt_made` | INTEGER | Unix seconds when the prompt was submitted. |
| `baseline_tree` | TEXT | Git tree SHA snapshot taken at submit time. |
| `ended_at` | INTEGER NULL | NULL = turn still open; set when the agent stops. |

**`diff`** — one row per file changed in a turn. (Table name is singular: `diff`.)

| Column | Type | Notes |
| --- | --- | --- |
| `diff_id` | TEXT PK | Random 16-hex-char id. |
| `turn_id` | TEXT FK → turns | Which turn produced this change. |
| `file_path` | TEXT | Repo-relative path (from git, forward slashes). |
| `changes_made` | TEXT | The unified diff patch text. **Maps to `Diff.Patch` in Go.** |
| `lines_added` | INTEGER | Count of `+` lines in the patch. |
| `lines_removed` | INTEGER | Count of `-` lines in the patch. |

Indexes exist on `turns(session_id)`, `diff(turn_id)`, and `diff(file_path)` to
keep the read queries fast.

### Entity relationship

```
┌────────────┐        ┌───────────────────┐        ┌──────────────────┐
│  sessions  │ 1    ∞ │       turns       │ 1    ∞ │       diff       │
│────────────│───────<│───────────────────│───────<│──────────────────│
│ session_id │        │ turn_id           │        │ diff_id          │
│ repo_path  │        │ session_id (FK)   │        │ turn_id (FK)     │
│ started_at │        │ prompt            │        │ file_path        │
└────────────┘        │ turn_number       │        │ changes_made     │
                      │ time_prompt_made  │        │ lines_added      │
                      │ baseline_tree     │        │ lines_removed    │
                      │ ended_at          │        └──────────────────┘
                      └───────────────────┘
```

### One naming gotcha to remember

The Go field `Diff.Patch` is stored in the SQL column `changes_made`. They are
the same thing (the patch text); only the names differ, for historical reasons.
Every query in `db.go` maps between them — keep that in mind if you add a query.

---

## 8. Package-by-package deep dive

### 8.1 `internal/db` — storage and queries

**Job:** own the SQLite connection and expose every read/write the rest of the
program needs. No other package writes SQL.

**Key types:**

- `DB` — wraps `*sql.DB` and remembers its own file path.
- `Session`, `Turn`, `Diff` — row structs. `Turn.EndedAt` is a `*time.Time` so
  that "still open" is representable as `nil`.
- `FileTouch` — a `Diff` **embedded** together with the prompt/turn that produced
  it. Returned by `FileHistory`, used by `blame`.

**Lifecycle:**

- `Open()` — resolves `~/.blackbox/sessions.db`, creates the directory, opens the
  connection, sets two PRAGMAs (`busy_timeout = 5000` so briefly-overlapping hook
  processes wait for the lock instead of erroring; `foreign_keys = ON`), then runs
  `migrate()`.
- `migrate()` — executes the `schema` (all `CREATE TABLE IF NOT EXISTS`), then
  calls `addColumnIfMissing` for `baseline_tree` and `ended_at`. That second step
  is **forward compatibility**: early dev databases were created before those
  columns existed, and `CREATE TABLE IF NOT EXISTS` will not add columns to an
  existing table. `addColumnIfMissing` reads `PRAGMA table_info` and only runs
  `ALTER TABLE ... ADD COLUMN` when the column is absent.

**Write methods:** `InsertSession` (idempotent via `ON CONFLICT DO NOTHING`),
`InsertTurn`, `InsertDiff`, `FinalizeTurn`.

**Read methods:** `ListSessions` (with a per-session turn count via `LEFT JOIN`),
`GetSession`, `GetTurns`, `GetTurn`, `OpenTurn` (latest turn where `ended_at IS
NULL`), `GetDiffsForTurn`, `FileHistory` (newest first, joined to its turn),
`Counts`, and the two prefix resolvers `SessionIDsWithPrefix` /
`TurnIDsWithPrefix` (so the CLI can accept the short 8-char ids it prints).

**Conventions if you add a query:**

- "Not found" is returned as `(nil, nil)`, not an error. Callers check for `nil`.
- Timestamps are stored as Unix seconds (`INTEGER`) and converted to `time.Time`
  on the way out.
- Nullable columns are scanned through `sql.NullString` / `sql.NullInt64` and
  then copied into the struct.
- Multi-row scanning of turns goes through the shared `scanTurns` helper — reuse
  it rather than duplicating the column list.

### 8.2 `internal/git` — the snapshot/diff/revert engine

**Job:** everything that shells out to `git`. This is the heart of the project;
[section 9](#9-the-capture-mechanism-in-depth) explains the core trick in detail.

**Internal helper:**

- `run(root, env, stdin, args...)` — runs `git` in directory `root`, optionally
  with extra environment and stdin, returns stdout, and folds stderr into the
  error so failures are readable. Every other function is built on this.

**Public API:**

- `Root(dir) (string, bool)` — `git rev-parse --show-toplevel`; the bool reports
  whether `dir` is inside a git work tree at all.
- `SnapshotTree(root) (string, error)` — records the current working tree as a
  git tree object **using a throwaway index** and returns its SHA. See section 9.
- `DiffTrees(root, treeA, treeB) ([]FileDiff, error)` — runs `git diff` between
  two tree SHAs and parses the output into one `FileDiff` per file.
- `ApplyReverse(root, patch) error` — undoes a stored patch against the working
  tree; dry-runs with `git apply --reverse --check` first so a conflicting revert
  fails cleanly instead of half-applying.

**`FileDiff`** is the value type carried around: `{ Path, Patch string; Added,
Removed int }`.

**Parsing:** `parseUnifiedDiff` splits a combined `git diff` on lines beginning
with `diff --git `, then for each file section pulls the path from the `+++ b/…`
or `--- a/…` line and counts `+`/`-` body lines. `pathFromHeader` is a fallback
for binary files (which emit no `+++`/`---` lines).

Diffs are produced with `--no-renames` on purpose: a rename becomes an explicit
delete+add, which reverse-applies predictably and keeps line counts honest.

### 8.3 `internal/capture` — events → turns & diffs

**Job:** translate a Claude Code hook payload into database writes. This is the
"business logic" of recording.

- `Event` — the subset of the hook JSON we read: `session_id`, `hook_event_name`,
  `cwd`, `prompt`. One struct handles every hook type because the type is carried
  in `hook_event_name`.
- `Recorder{ DB *db.DB }` — holds the database handle.
- `Recorder.Handle(payload []byte)` — unmarshals the JSON and dispatches on
  `HookEventName`:
  - `UserPromptSubmit` → `onPromptSubmit`
  - `Stop` / `SubagentStop` → `onStop`
  - anything else → ignored (returns nil), so new Claude hooks never break us.

**`onPromptSubmit`:** ensure the session row exists, take a baseline `SnapshotTree`
(only if we're in a git repo — otherwise the prompt is still recorded with an
empty baseline), compute the next turn number, and `InsertTurn`.

**`onStop`:** find the open turn via `OpenTurn`. If none, do nothing (e.g. a Stop
with no matching prompt in a resumed session). If the turn has a baseline and
we're in a repo, `recordDiff` (snapshot again, diff against the baseline, insert
one row per file). Finally `FinalizeTurn` regardless — even a prompt that changed
nothing gets closed.

`newID()` produces the 16-hex-char ids using `crypto/rand`.

### 8.4 `internal/hooks` — installing into settings.json

**Job:** make `blackbox init` idempotent and non-destructive.

- `Events = []string{"UserPromptSubmit", "Stop"}` — what we register.
- `Install(projectDir, exePath) (Report, error)` — ensures `<projectDir>/.claude/
  settings.json` contains a hook entry for each event pointing at
  `"<exePath>" hook`. It:
  1. reads existing settings into a generic `map[string]any` (empty if the file
     is absent or blank),
  2. for each event, checks whether our command is already present
     (`hasCommand`) and only appends a new hook group if not,
  3. writes the merged JSON back, pretty-printed.

Because it merges a generic map, **unrelated settings and other tools' hooks are
preserved.** Re-running `init` reports events as `Existing` and changes nothing.
The three tests in `hooks_test.go` lock in create / idempotency / preservation.

The command string is built with `%q` (`"<path>" hook`) so paths with spaces
survive the shell that Claude Code uses to run hooks.

### 8.5 `internal/ui` — the `serve` web timeline

**Job:** a read-only, dependency-free web view. No JS framework, no external
assets — one HTML template with embedded CSS.

- `Serve(database, addr)` — parses the template, wires two routes on an
  `http.ServeMux`, prints the URL, and blocks in `http.ListenAndServe`.
- `GET /` (`handleIndex`) — lists sessions (`ListSessions`).
- `GET /session?id=…` (`handleSession`) — one session's turns and their diffs.

The handlers build **view-model structs** (`pageData`, `sessionRow`,
`sessionDetail`, `turnView`, `diffView`, `diffLine`) and hand them to the
template. `classifyPatch` tags every diff line with a CSS class (`add`, `del`,
`hunk`, `meta`, `ctx`) so the template can colour it. Because line text is passed
as data and rendered with `{{.Text}}`, `html/template` auto-escapes it — no XSS
risk from patch content.

### 8.6 `cmd/BlackBox` — the CLI

Split into two files, both `package main`:

**`main.go`** — the entry point.

- `main()` switches on `os.Args[1]`. Unknown commands print usage and exit 1.
- `runHook()` is special: it is what the Claude hooks call. It reads stdin, opens
  the DB, and calls `capture.Recorder.Handle`. **It swallows every error** (logs
  to `~/.blackbox/hook.log` via `logHookError`) and never exits non-zero, because
  a non-zero exit from a `UserPromptSubmit` hook would *block the developer's
  prompt*. It also never writes to stdout, which Claude would treat as added
  context.
- `usage()` prints the help text; `requireArg` enforces required positional args.

**`commands.go`** — one `runX` function per subcommand plus shared helpers.

Notable helpers:

- `openDB()` / `fatal()` — the standard "open the DB or die with a message" pair
  used by every read command.
- `resolveSession` / `resolveTurn` — accept a full id **or any unique prefix**;
  ambiguous or missing prefixes produce a friendly fatal message.
- `parseFileLine` — splits `path:42` on the **last** colon so paths containing
  colons still parse.
- `repoRelative` — converts a user-typed path into the repo-relative form stored
  in the DB, resolving symlinks on both sides (so macOS `/tmp` → `/private/tmp`
  doesn't break matching).
- `patchCoversNewLine` / `newSideRange` — parse `@@ … +start,count @@` hunk
  headers to decide whether a diff touched a specific line (this is what makes
  `blame` line-precise).
- `short`, `firstLine`, `compactPath`, `stamp` — presentation helpers.

---

## 9. The capture mechanism, in depth

This is the part worth understanding deeply, because it is the whole reason the
project is interesting and it is the easiest place to introduce a subtle bug.

### The requirement

Capture the diff a single prompt produced, **without disturbing the developer's
own git state** — in particular, without touching what they have staged in their
index.

### Why the obvious approaches fail

- `git diff` shows the *cumulative* difference from the last commit, not the delta
  attributable to one prompt.
- `git add` / `git stash` to snapshot state would **overwrite the developer's
  staging area**, which is unacceptable — they may have carefully staged a subset
  of changes.
- We can't keep an in-memory "before" image because each hook is a *separate
  process*.

### The trick: a throwaway index + `write-tree`

Git's index (staging area) is just a file. Git lets you point at a *different*
index file via the `GIT_INDEX_FILE` environment variable. So BlackBox snapshots
the working tree into a **temporary, disposable index** and never goes near the
real one:

```go
// internal/git/git.go — SnapshotTree, paraphrased
tmp := <a fresh temp file path>
seedTempIndex(root, tmp)                       // copy the real index in for speed
env := append(os.Environ(), "GIT_INDEX_FILE="+tmp)
run(root, env, "", "add", "-A")                // stage the whole work tree → temp index
tree := run(root, env, "", "write-tree")       // hash it into a tree object
// tmp is deleted on return
```

`git write-tree` turns the contents of that temporary index into a **tree
object** in git's object database and prints its SHA. A tree object is an
immutable snapshot of a directory's contents. Two such SHAs can be diffed
directly:

```
patch = git diff <baselineTreeSHA> <finalTreeSHA>
```

That patch is exactly what changed between prompt-submit and agent-stop —
i.e. what the prompt did. Because it is a normal unified diff, undoing it is just:

```
git apply --reverse <patch>
```

### Why `seedTempIndex`

Starting from a *completely empty* temp index would force `git add -A` to re-hash
every file in the repo on every snapshot. Instead we copy the real index
(located via `git rev-parse --git-path index`) into the temp file first, so git's
stat cache lets it skip unchanged files. It's a pure performance optimization —
correctness is identical if the copy fails, so `seedTempIndex` is best-effort and
ignores errors.

### The safety guarantee (and its test)

The real index is **never** passed to any command — only the temp path is. The
test `TestSnapshotLeavesIndexUntouched` in `git_test.go` stages a change, takes a
snapshot, and asserts `git status --porcelain` is byte-for-byte identical before
and after. If you change `SnapshotTree`, that test must still pass.

`TestSnapshotDiffReverse` covers the full contract: snapshot → edit → snapshot →
diff → reverse-apply restores the original content (including deleting files that
the turn created).

### What "ignored" files do (a nice side effect)

`git add -A` respects `.gitignore`, so build artifacts and `.blackbox/` never
enter a snapshot. Diffs only ever cover tracked-or-untracked-but-not-ignored
files — which is exactly what you want.

---

## 10. Command walkthroughs

Each command's call path, so you can find the code fast.

| Command | Entry | What it calls |
| --- | --- | --- |
| `init` | `runInit` | `os.Executable` → `hooks.Install(wd, exe)` → `db.Open` (warm the DB) → `git.Root` (warn if not a repo). |
| `hook` | `runHook` | `io.ReadAll(stdin)` → `db.Open` → `capture.Recorder.Handle`. Never fails loudly. |
| `log` | `runLog` | `db.ListSessions` → tabwriter table. |
| `show <id>` | `runShow` | `resolveSession` → `db.GetTurns` → `db.GetDiffsForTurn` per turn. |
| `revert <id>` | `runRevert` | `resolveTurn` → `db.GetSession` → `git.Root` → `db.GetDiffsForTurn` → concatenate patches → `git.ApplyReverse`. |
| `blame <file:line>` | `runBlame` | `parseFileLine` → `repoRelative` → `db.FileHistory` → `patchCoversNewLine` to pick the line-precise turn. |
| `status` | `runStatus` | `db.Counts` + `db.Path`. |
| `serve` | `runServe` | `ui.Serve` → `ListSessions` / `GetSession` / `GetTurns` / `GetDiffsForTurn`. |

**`blame` line-precision, spelled out:** `FileHistory` returns every turn that
touched the file, newest first. If a line number was given, BlackBox walks that
list and returns the newest turn whose patch has a hunk whose *new-side* range
covers the line (`patchCoversNewLine`). If no hunk covers the line exactly, it
falls back to the most recent change to the file and says so.

**`revert` safety:** all of a turn's per-file patches are concatenated and passed
to `git apply --reverse`, which is preceded by a `--check` dry run. If the code
has changed since the turn was recorded such that the reverse would not apply
cleanly, the whole thing is refused — nothing is half-applied. Revert edits the
working tree only; it does not commit.

---

## 11. Claude Code hooks integration

BlackBox is driven entirely by [Claude Code hooks](https://docs.claude.com/en/docs/claude-code/hooks).
A hook is a shell command Claude Code runs at a lifecycle event, handing it a JSON
payload on **stdin**.

### What `init` writes

`blackbox init` merges this into `<project>/.claude/settings.json` (project
settings — the shared file, not `settings.local.json`):

```json
{
  "hooks": {
    "UserPromptSubmit": [
      { "hooks": [ { "type": "command", "command": "\"/abs/path/to/blackbox\" hook" } ] }
    ],
    "Stop": [
      { "hooks": [ { "type": "command", "command": "\"/abs/path/to/blackbox\" hook" } ] }
    ]
  }
}
```

The absolute path comes from `os.Executable()` at init time, so the hooks call
the exact binary you ran `init` with. (Move the binary later → re-run `init`.)

### The payloads we read

`UserPromptSubmit` (fired when you submit a prompt):

```json
{ "session_id": "…", "hook_event_name": "UserPromptSubmit", "cwd": "/repo", "prompt": "add a cache" }
```

`Stop` (fired when the agent finishes responding):

```json
{ "session_id": "…", "hook_event_name": "Stop", "cwd": "/repo" }
```

We read `session_id`, `hook_event_name`, `cwd`, and (for prompts) `prompt`. Extra
fields Claude sends are ignored.

### Why these two hooks

They are the exact boundaries of a turn: `UserPromptSubmit` is the "before"
moment, `Stop` is the "after" moment. Everything the agent did in between is the
turn's diff.

### Two integration rules that must never be broken

1. **The `hook` command must exit 0**, always. A non-zero exit on
   `UserPromptSubmit` *blocks the user's prompt*. `runHook` guarantees this.
2. **The `hook` command must not print to stdout.** For `UserPromptSubmit`,
   stdout is injected into Claude's context. Diagnostics go to
   `~/.blackbox/hook.log` instead.

### A known dormant detail

`capture.Handle` also routes `SubagentStop` to `onStop`, but `hooks.Events` only
registers `UserPromptSubmit` and `Stop`, so `SubagentStop` is never actually
delivered today. The routing is harmless and left in place as a forward-compat
hook point (see [section 13](#13-how-to-make-common-changes) if you want to
capture subagent turns).

---

## 12. Build, run, and test

```bash
# Build everything
go build ./...

# Build the CLI binary (note: the directory is cmd/BlackBox)
go build -o blackbox ./cmd/BlackBox

# Run the tests (git round-trip + index safety, hook install)
go test ./...

# Static checks / formatting — keep these clean before committing
go vet ./...
gofmt -l cmd/ internal/     # prints nothing when all files are formatted
```

Requirements: Go 1.25+ and `git` on your `PATH`. There is **no CGO** — the SQLite
driver is the pure-Go `modernc.org/sqlite`, which is why the tool ships as a
single static-ish binary with no system dependencies.

---

## 13. How to make common changes

Recipes for the changes you're most likely to make. Follow the existing patterns.

### Add a new CLI command

1. Add a `case "<name>":` to the switch in `cmd/BlackBox/main.go`, calling a new
   `run<Name>` function (and `requireArg` if it takes an argument).
2. Implement `run<Name>` in `cmd/BlackBox/commands.go`. Use `openDB()` + `defer
   database.Close()` and `fatal(...)` for errors, like the others.
3. Add a line to the `usage()` text in `main.go`.

### Add a new query

Put it in `internal/db/db.go` as a method on `*DB`. Return `(nil, nil)` for
not-found, convert Unix ints to `time.Time`, and reuse `scanTurns` if you're
returning turns. Don't write SQL anywhere outside this package.

### Capture a new hook event

1. Add the event name to `internal/hooks/hooks.go` `Events` so `init` registers
   it. (Users must re-run `blackbox init`.)
2. Handle it in `internal/capture/capture.go` `Handle`'s switch, adding fields to
   `Event` if you need more of the payload.
3. If it needs new storage, extend the schema (next recipe).

### Change the database schema

1. Edit the `schema` constant in `internal/db/db.go`.
2. For a **new column on an existing table**, also add it to the loop in
   `migrate()` (via `addColumnIfMissing`) so existing databases upgrade in place.
   `CREATE TABLE IF NOT EXISTS` alone will *not* alter an existing table.
3. Update the affected row struct and every query that lists that table's
   columns.

### Change what the web UI shows

Everything is in `internal/ui/ui.go`. Add fields to the view-model structs in the
handlers, then reference them in the `pageTemplate` string. If you render
diff/patch text, pass it as data (not `template.HTML`) so it stays auto-escaped.

### A change checklist before you commit

- [ ] `go build ./...` and `go test ./...` pass
- [ ] `go vet ./...` clean, `gofmt -l` prints nothing
- [ ] If you touched `SnapshotTree`, the two `git_test.go` tests still pass
- [ ] If you touched capture/hooks, you tested the pipeline end-to-end
      (see the isolated-`HOME` recipe below)

### Testing the full pipeline safely

The real database is `~/.blackbox/sessions.db`. To exercise `hook` without
polluting it, run against an isolated `HOME` and feed the binary the JSON that
Claude would send. This mirrors how the pipeline was validated during
development:

```bash
BB=/tmp/bbtest; rm -rf "$BB"; mkdir -p "$BB/home" "$BB/repo"
BIN="$BB/blackbox"; go build -o "$BIN" ./cmd/BlackBox
export HOME="$BB/home"                       # isolate ~/.blackbox
REPO="$BB/repo"
git -C "$REPO" init -q
git -C "$REPO" -c user.email=t@t.co -c user.name=t commit -q --allow-empty -m init
printf 'package main\nfunc main(){}\n' > "$REPO/main.go"

SID=test-1
# Turn: prompt → edit → stop
printf '{"session_id":"%s","hook_event_name":"UserPromptSubmit","cwd":"%s","prompt":"add x"}' "$SID" "$REPO" | "$BIN" hook
printf 'package main\nvar x = 1\nfunc main(){}\n' > "$REPO/main.go"
printf '{"session_id":"%s","hook_event_name":"Stop","cwd":"%s"}' "$SID" "$REPO" | "$BIN" hook

"$BIN" show "$SID"       # should show the turn and main.go +1
```

Because `db.Open` derives its path from `HOME`, overriding `HOME` fully isolates
the test. Delete `$BB` when done.

> Note: this repo is intentionally **not** self-instrumented — nobody has run
> `blackbox init` here, so `.claude/settings.json` has no BlackBox hooks. If you
> want BlackBox to record work *on BlackBox itself*, run `init` with a binary
> built somewhere outside the repo, and remember it will modify
> `.claude/settings.json`.

---

## 14. Design decisions & FAQ

**Why snapshot at Stop instead of after every tool call (`PostToolUse`)?**
A turn = a prompt and everything it caused. Diffing once at Stop captures the net
change of the whole turn with one clean before/after pair, and avoids the "what
was the state before this individual edit?" bookkeeping that per-tool capture
needs. The trade-off: manual edits you make *between* turns are attributed to
neither turn (they happen after one turn's "after" snapshot and before the next
turn's "before" snapshot).

**Why store real unified diffs instead of before/after blobs?**
Because `revert` then costs one line (`git apply --reverse`) and the patches are
human-readable in `show`/`serve`. Blob storage would need us to reimplement
diff/patch.

**Why `modernc.org/sqlite` (pure Go) instead of `mattn/go-sqlite3` (CGO)?**
No C toolchain, trivial cross-compilation, single self-contained binary. For a
local single-writer tool the performance difference is irrelevant.

**Why raw `net/http` and `html/template`, no framework?**
The UI is a two-page read-only view. The standard library does it with zero
dependencies and no build step, and `html/template` gives safe escaping for free.

**Why require git?**
The whole diff/revert model *is* git tree objects. Without a repo we degrade
gracefully — prompts are still recorded — but diffs, `revert`, and `blame` are
unavailable, and `init` says so.

**Why does the diff table name use the singular `diff`?**
Historical; it's the name the schema shipped with. Don't "fix" it without a
migration — existing databases use it.

---

## 15. Known limitations & edge cases

- **Between-turn manual edits are unattributed** (see above).
- **Overlapping open turns.** If a second `UserPromptSubmit` somehow arrived
  before the previous turn's `Stop`, `OpenTurn` finalizes the *newest* open turn,
  leaving the older one open forever. Claude Code fires these serially, so this
  doesn't happen in practice.
- **Revert conflicts.** If you (or a later prompt) changed the same lines, the
  `--check` dry run refuses the reverse-apply. This is intentional — better a
  clean failure than a mangled file.
- **Binary files.** Recorded, but line counts are 0 and reverse-applying a binary
  patch is not guaranteed. Text is the happy path.
- **Path edge cases.** Paths with unusual characters rely on git's default
  quoting; typical source paths are fine.
- **The tool is Claude-Code-specific at the capture layer.** The storage and
  query layers are agent-agnostic; only `internal/capture` and `internal/hooks`
  know about Claude Code.

---

## 16. Glossary

- **Baseline tree** — git tree SHA snapshot taken when a prompt is submitted.
- **Diff (row)** — one file's unified patch within a turn (`changes_made`).
- **Hook** — a command Claude Code runs at a lifecycle event, with JSON on stdin.
- **Index / staging area** — git's list of what will go in the next commit.
  BlackBox uses a *throwaway* copy of it and never mutates the real one.
- **Open turn** — a turn whose `ended_at` is NULL (agent still working).
- **Session** — one Claude Code session (`session_id`), scoped to a repo.
- **Tree object** — an immutable git snapshot of a directory's contents,
  addressed by SHA; produced here by `git write-tree`.
- **Turn** — one prompt plus the diff it produced. The core unit.
- **Unified diff** — the standard `git`/`diff` patch format; what `revert`
  reverse-applies.

---

*Keep this document honest: when you change the code, change the section here that
describes it. A wrong architecture doc is worse than none.*
