// Command blackbox records what an AI coding agent did to a repository — prompt
// by prompt, diff by diff — so a session can be audited, replayed, or reverted.
//
// It runs as a thin CLI. `blackbox init` installs Claude Code hooks that call
// `blackbox hook` on every prompt and stop event; the remaining subcommands
// read back the recorded audit trail.
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/mkdirRON/BlackBox/internal/capture"
	"github.com/mkdirRON/BlackBox/internal/db"
	_ "modernc.org/sqlite"
)

// listenAddr is where `blackbox serve` exposes the timeline UI.
const listenAddr = "localhost:7331"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}
	args := os.Args[2:]

	switch os.Args[1] {
	case "hook":
		// Invoked by Claude Code hooks. Must never fail the user's session:
		// it swallows every error and exits 0.
		runHook()
	case "init":
		runInit()
	case "serve":
		runServe()
	case "log":
		runLog()
	case "show":
		requireArg(args, "show", "<session-id>")
		runShow(args[0])
	case "revert":
		requireArg(args, "revert", "<turn-id>")
		runRevert(args[0])
	case "blame":
		requireArg(args, "blame", "<file:line>")
		runBlame(args[0])
	case "status":
		runStatus()
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", os.Args[1])
		usage()
		os.Exit(1)
	}
}

// runHook reads a hook payload from stdin and records it. All failures are
// logged to ~/.blackbox/hook.log and swallowed so a BlackBox problem can never
// block a Claude Code prompt. Nothing is written to stdout, which Claude would
// otherwise treat as added context.
func runHook() {
	payload, err := io.ReadAll(os.Stdin)
	if err != nil {
		logHookError(err)
		return
	}
	database, err := db.Open()
	if err != nil {
		logHookError(err)
		return
	}
	defer database.Close()

	rec := &capture.Recorder{DB: database}
	if err := rec.Handle(payload); err != nil {
		logHookError(err)
	}
}

func logHookError(err error) {
	home, e := os.UserHomeDir()
	if e != nil {
		return
	}
	f, e := os.OpenFile(filepath.Join(home, ".blackbox", "hook.log"),
		os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if e != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s  %v\n", time.Now().Format(time.RFC3339), err)
}

func requireArg(args []string, name, argHint string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "usage: blackbox %s %s\n", name, argHint)
		os.Exit(1)
	}
}

func usage() {
	fmt.Print(`BlackBox — an audit trail for AI coding sessions

usage: blackbox <command> [args]

commands:
  init                 install capture hooks into ./.claude/settings.json
  serve                open the session timeline at http://localhost:7331
  log                  list recorded sessions
  show   <session-id>  show a session's prompts and the diffs they produced
  revert <turn-id>     undo the changes a single prompt made (git apply --reverse)
  blame  <file:line>   show which prompt last changed a line
  status               show database location and counts

session and turn ids may be abbreviated to any unique prefix.
`)
}
