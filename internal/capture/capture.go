// Package capture turns Claude Code hook events into BlackBox's audit trail.
//
// Two hooks drive everything:
//
//	UserPromptSubmit -> open a turn: record the prompt and snapshot the working
//	                    tree as a baseline.
//	Stop             -> close the turn: snapshot the tree again, diff it against
//	                    the baseline, and store the per-file patch the prompt
//	                    produced.
//
// Hooks run as short-lived processes, so all state lives in the database.
package capture

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"

	"github.com/mkdirRON/BlackBox/internal/db"
	"github.com/mkdirRON/BlackBox/internal/git"
)

// Event is the subset of every Claude Code hook payload BlackBox reads. The
// hook type is carried in HookEventName, so a single command handles them all.
type Event struct {
	SessionID     string `json:"session_id"`
	HookEventName string `json:"hook_event_name"`
	Cwd           string `json:"cwd"`
	Prompt        string `json:"prompt"` // present on UserPromptSubmit
}

// Recorder writes captured events to the database.
type Recorder struct {
	DB *db.DB
}

// Handle parses a hook payload and dispatches on its event type. Unknown events
// are ignored so new Claude Code hooks never cause failures.
func (r *Recorder) Handle(payload []byte) error {
	var ev Event
	if err := json.Unmarshal(payload, &ev); err != nil {
		return fmt.Errorf("decode hook payload: %w", err)
	}
	if ev.SessionID == "" {
		return fmt.Errorf("hook payload missing session_id")
	}

	switch ev.HookEventName {
	case "UserPromptSubmit":
		return r.onPromptSubmit(ev)
	case "Stop", "SubagentStop":
		return r.onStop(ev)
	default:
		return nil
	}
}

// onPromptSubmit opens a new turn and records the baseline tree.
func (r *Recorder) onPromptSubmit(ev Event) error {
	repoPath := ev.repoPath()
	if err := r.DB.InsertSession(ev.SessionID, repoPath); err != nil {
		return err
	}

	var baseline string
	if root, ok := git.Root(repoPath); ok {
		// Best effort: a failed snapshot still records the prompt, just
		// without a diff at the end of the turn.
		if tree, err := git.SnapshotTree(root); err == nil {
			baseline = tree
		}
	}

	turnNumber, err := r.DB.NextTurnNumber(ev.SessionID)
	if err != nil {
		return err
	}
	return r.DB.InsertTurn(newID(), ev.SessionID, ev.Prompt, turnNumber, baseline)
}

// onStop closes the open turn, recording the diff it produced.
func (r *Recorder) onStop(ev Event) error {
	turn, err := r.DB.OpenTurn(ev.SessionID)
	if err != nil {
		return err
	}
	if turn == nil {
		return nil // Stop with no matching prompt (e.g. a resumed session)
	}

	if turn.BaselineTree != "" {
		if root, ok := git.Root(ev.repoPath()); ok {
			if err := r.recordDiff(root, turn); err != nil {
				return err
			}
		}
	}
	return r.DB.FinalizeTurn(turn.TurnID)
}

// recordDiff snapshots the tree now and stores each file that changed since the
// turn's baseline.
func (r *Recorder) recordDiff(root string, turn *db.Turn) error {
	current, err := git.SnapshotTree(root)
	if err != nil {
		return err
	}
	if current == turn.BaselineTree {
		return nil // the prompt changed no files
	}

	files, err := git.DiffTrees(root, turn.BaselineTree, current)
	if err != nil {
		return err
	}
	for _, f := range files {
		if err := r.DB.InsertDiff(newID(), turn.TurnID, f.Path, f.Patch, f.Added, f.Removed); err != nil {
			return err
		}
	}
	return nil
}

// repoPath resolves the working directory for an event, falling back to the
// process CWD when the hook omits it.
func (ev Event) repoPath() string {
	if ev.Cwd != "" {
		return ev.Cwd
	}
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return "."
}

// newID returns a random 16-hex-character identifier.
func newID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}
