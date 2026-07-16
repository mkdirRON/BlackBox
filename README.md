# BlackBox

**An audit trail for AI coding sessions — diff by diff, prompt by prompt.**

BlackBox records exactly what your AI coding agent did to your codebase: every
prompt you send, paired with the exact file changes that prompt produced. When
something breaks, you can see *which prompt* caused it, replay the session as a
timeline, revert a single prompt's changes, or ask which prompt last touched a
line.

---

## Why

AI coding tools (Claude Code, Cursor, Copilot) generate code faster than anyone
can review it. Git shows you the *final state* of your working tree, but not the
reasoning chain that produced it:

- After a run of prompts, it's hard to see what actually changed at each step and
  whether it was intentional.
- When a bug appears, you don't know which prompt introduced it.
- One commit often bundles the work of a dozen prompts, so `git blame` points at
  "the AI session," not at the specific instruction that changed a line.

Git wasn't designed for prompt-linked diffs. BlackBox is. It sits alongside your
AI session and keeps a per-prompt ledger:

> Prompt 1 added the greeting · Prompt 2 introduced the cache · Prompt 7 changed
> the auth check.

---

## How it works

BlackBox plugs into [Claude Code's hook system](https://docs.claude.com/en/docs/claude-code/hooks).
`blackbox init` registers two hooks in your project's `.claude/settings.json`:

| Hook               | What BlackBox does                                                        |
| ------------------ | ------------------------------------------------------------------------- |
| `UserPromptSubmit` | Opens a **turn**: stores the prompt and snapshots the working tree.       |
| `Stop`             | Closes the turn: snapshots the tree again and stores the diff in between. |

A "turn" is one prompt and the complete set of file changes the agent made while
answering it.

### Non-intrusive snapshots

To capture a diff without disturbing your work, BlackBox snapshots the working
tree into a **throwaway git index** (`GIT_INDEX_FILE`) and records the resulting
tree object:

```
git add -A            # into a temporary index, never your real one
git write-tree        # -> a tree SHA for the current working-tree state
```

Diffing the baseline tree (prompt submitted) against the final tree (agent
stopped) yields the exact per-file patch that prompt produced. Because the
patches are real unified diffs, `blackbox revert` is just `git apply --reverse`.
Your staging area is never touched — this is covered by a test.

Everything is stored locally in a SQLite database at `~/.blackbox/sessions.db`.
Nothing leaves your machine.

---

## Install

BlackBox is a single Go binary (pure-Go SQLite, so no CGO or system libraries).

```bash
git clone https://github.com/mkdirRON/BlackBox
cd BlackBox
go build -o blackbox ./cmd/BlackBox

# put it on your PATH, e.g.
mv blackbox /usr/local/bin/
```

Requirements: Go 1.25+ to build, and `git` on your PATH for diff capture,
revert, and blame.

---

## Quick start

```bash
cd your-project
blackbox init          # registers the hooks in ./.claude/settings.json
```

Restart Claude Code in that directory (hooks load at session start), then code
as usual. Every prompt and its diff is now recorded. To review:

```bash
blackbox log           # list sessions
blackbox show <id>     # timeline of one session
blackbox serve         # browse it at http://localhost:7331
```

---

## Commands

```
blackbox init                 install capture hooks into ./.claude/settings.json
blackbox serve                open the session timeline at http://localhost:7331
blackbox log                  list recorded sessions
blackbox show   <session-id>  show a session's prompts and the diffs they produced
blackbox revert <turn-id>     undo the changes a single prompt made
blackbox blame  <file:line>   show which prompt last changed a line
blackbox status               show database location and counts
```

Session and turn ids may be abbreviated to any unique prefix.

### `blackbox show`

```
$ blackbox show smoke
session smoke-se  (~/code/demo)  started 2026-07-15 23:07

  #1  2026-07-15 23:07  turn 6fbc8b96
      > add a greeting to main
      main.go                                      +3 -1

  #2  2026-07-15 23:07  turn 8f2a63b1
      > add a cache file for lookups
      cache.go                                     +3 -0
```

### `blackbox blame`

Line-precise attribution — it reads each turn's diff hunks to find the prompt
that actually changed that line:

```
$ blackbox blame cache.go:3
cache.go:3 was last changed by:
  turn    8f2a63b1  (session smoke-se, turn #2)
  when    2026-07-15 23:07
  prompt  add a cache file for lookups
  change  +3 -0
```

### `blackbox revert`

Reverse-applies just that one prompt's changes to your working tree. It dry-runs
with `git apply --check` first, so a revert that would conflict fails cleanly
instead of half-applying:

```
$ blackbox revert 8f2a63b1
reverted turn 8f2a63b1 ("add a cache file for lookups")
  restored cache.go (undid +3 -0)
review with `git diff`, then commit when you're happy.
```

### `blackbox serve`

A local, read-only web timeline: sessions on the index, and for each session the
prompts with their colourised, per-file diffs.

---

## Data model

One SQLite database, three tables:

- **sessions** — one row per Claude Code session (`session_id`, repo path, start time).
- **turns** — one row per prompt (`prompt`, `turn_number`, baseline tree SHA, timestamps).
- **diff** — one row per file changed in a turn (`file_path`, the unified patch,
  lines added/removed).

```
sessions 1───∞ turns 1───∞ diff
```

---

## Notes & limitations

- **Git is required for diffs.** Without a git repo, prompts are still recorded,
  but diffs, `revert`, and `blame` are unavailable. `init` warns you if the
  directory isn't a repo.
- **Turn granularity.** A turn's diff is the *net* change to the working tree
  between prompt submission and the agent stopping. Edits you make by hand
  between turns aren't attributed to either prompt.
- **Revert operates on the working tree.** It restores files but doesn't commit;
  review with `git diff` and commit when satisfied. If the recorded code has
  since changed, the reverse-apply is refused rather than forced.
- **Hooks never block your session.** If BlackBox hits an error inside a hook it
  logs to `~/.blackbox/hook.log` and exits cleanly — it will never interrupt a
  Claude Code prompt.
- **Claude Code specific.** The capture layer targets Claude Code's hook events
  today; the storage and query layers are agent-agnostic.

---

## Development

```bash
go build ./...     # build everything
go test ./...      # unit tests (git round-trip + safety, hook installation)
go vet ./...
```

For a top-to-bottom explanation of the codebase — architecture, data model, the
capture mechanism, and how to contribute — see **[ARCHITECTURE.md](ARCHITECTURE.md)**.

Layout:

```
cmd/BlackBox/        CLI entry point and command implementations
internal/capture/    turns hook events into recorded turns + diffs
internal/git/        working-tree snapshots, tree diffing, reverse-apply
internal/hooks/      merge-installs hooks into .claude/settings.json
internal/db/         SQLite schema and queries
internal/ui/         the `serve` web timeline
```
