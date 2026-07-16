package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/mkdirRON/BlackBox/internal/db"
	"github.com/mkdirRON/BlackBox/internal/git"
	"github.com/mkdirRON/BlackBox/internal/hooks"
	"github.com/mkdirRON/BlackBox/internal/ui"
)

const stamp = "2006-01-02 15:04"

// runInit installs the capture hooks into the current project.
func runInit() {
	exe, err := os.Executable()
	if err != nil {
		fatal("cannot resolve blackbox binary path: %v", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	wd, err := os.Getwd()
	if err != nil {
		fatal("cannot determine working directory: %v", err)
	}

	rep, err := hooks.Install(wd, exe)
	if err != nil {
		fatal("installing hooks: %v", err)
	}

	fmt.Printf("BlackBox hooks written to %s\n", rep.SettingsPath)
	if len(rep.Added) > 0 {
		fmt.Printf("  added:   %s\n", strings.Join(rep.Added, ", "))
	}
	if len(rep.Existing) > 0 {
		fmt.Printf("  present: %s\n", strings.Join(rep.Existing, ", "))
	}
	fmt.Printf("  command: %s\n", rep.Command)

	// Create the database now so the first hook has nothing to set up.
	if database, err := db.Open(); err == nil {
		database.Close()
	}

	if _, ok := git.Root(wd); !ok {
		fmt.Println("\nnote: this directory is not a git repository. prompts will be recorded,")
		fmt.Println("but diffs, revert, and blame need git — run `git init` to enable them.")
	}
	fmt.Println("\nDone. Restart Claude Code in this directory and your session will be recorded.")
}

// runServe starts the timeline web UI.
func runServe() {
	database := openDB()
	defer database.Close()
	if err := ui.Serve(database, listenAddr); err != nil {
		fatal("serve: %v", err)
	}
}

// runLog lists recorded sessions, newest first.
func runLog() {
	database := openDB()
	defer database.Close()

	sessions, err := database.ListSessions()
	if err != nil {
		fatal("listing sessions: %v", err)
	}
	if len(sessions) == 0 {
		fmt.Println("no sessions recorded yet. run `blackbox init` in a repo, then use Claude Code.")
		return
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "SESSION\tREPO\tSTARTED\tTURNS")
	for _, s := range sessions {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\n",
			short(s.SessionID), compactPath(s.RepoPath), s.StartedAt.Format(stamp), s.TurnCount)
	}
	tw.Flush()
}

// runShow prints every prompt in a session and the files each one changed.
func runShow(idOrPrefix string) {
	database := openDB()
	defer database.Close()

	session := resolveSession(database, idOrPrefix)
	turns, err := database.GetTurns(session.SessionID)
	if err != nil {
		fatal("loading turns: %v", err)
	}

	fmt.Printf("session %s  (%s)  started %s\n",
		short(session.SessionID), compactPath(session.RepoPath), session.StartedAt.Format(stamp))
	if len(turns) == 0 {
		fmt.Println("  (no turns recorded)")
		return
	}

	for _, t := range turns {
		open := ""
		if t.EndedAt == nil {
			open = "  [open]"
		}
		fmt.Printf("\n  #%d  %s  turn %s%s\n", t.TurnNumber, t.CreatedAt.Format(stamp), short(t.TurnID), open)
		printPrompt(t.Prompt)

		diffs, err := database.GetDiffsForTurn(t.TurnID)
		if err != nil {
			fatal("loading diffs: %v", err)
		}
		if len(diffs) == 0 {
			fmt.Println("      (no file changes)")
			continue
		}
		for _, d := range diffs {
			fmt.Printf("      %-44s +%d -%d\n", d.FilePath, d.LinesAdded, d.LinesRemoved)
		}
	}
}

// runRevert reverse-applies every diff a single turn produced.
func runRevert(idOrPrefix string) {
	database := openDB()
	defer database.Close()

	turn := resolveTurn(database, idOrPrefix)
	session, err := database.GetSession(turn.SessionID)
	if err != nil || session == nil {
		fatal("cannot find the session for turn %s", short(turn.TurnID))
	}

	root, ok := git.Root(session.RepoPath)
	if !ok {
		fatal("%s is not a git repository; cannot revert", session.RepoPath)
	}

	diffs, err := database.GetDiffsForTurn(turn.TurnID)
	if err != nil {
		fatal("loading diffs: %v", err)
	}
	if len(diffs) == 0 {
		fmt.Printf("turn %s changed no files — nothing to revert.\n", short(turn.TurnID))
		return
	}

	var patch strings.Builder
	for _, d := range diffs {
		patch.WriteString(d.Patch)
	}
	if err := git.ApplyReverse(root, patch.String()); err != nil {
		fatal("revert failed: %v", err)
	}

	fmt.Printf("reverted turn %s (%q)\n", short(turn.TurnID), firstLine(turn.Prompt))
	for _, d := range diffs {
		fmt.Printf("  restored %s (undid +%d -%d)\n", d.FilePath, d.LinesAdded, d.LinesRemoved)
	}
	fmt.Println("review with `git diff`, then commit when you're happy.")
}

// runBlame reports which prompt last changed a file (or a specific line).
func runBlame(target string) {
	database := openDB()
	defer database.Close()

	file, line := parseFileLine(target)
	rel := repoRelative(file)

	touches, err := database.FileHistory(rel)
	if err != nil {
		fatal("loading history: %v", err)
	}
	if len(touches) == 0 {
		fmt.Printf("no recorded AI changes for %s\n", rel)
		return
	}

	// Prefer the newest turn whose diff actually covers the requested line.
	if line > 0 {
		for _, ft := range touches {
			if patchCoversNewLine(ft.Patch, line) {
				reportBlame(ft, rel, line, true)
				return
			}
		}
		fmt.Printf("no recorded turn changed %s:%d exactly; showing the most recent change to %s\n\n", rel, line, rel)
	}
	reportBlame(touches[0], rel, line, false)
}

func reportBlame(ft db.FileTouch, rel string, line int, exact bool) {
	where := rel
	if line > 0 && exact {
		where = fmt.Sprintf("%s:%d", rel, line)
	}
	fmt.Printf("%s was last changed by:\n", where)
	fmt.Printf("  turn    %s  (session %s, turn #%d)\n", short(ft.TurnID), short(ft.SessionID), ft.TurnNumber)
	fmt.Printf("  when    %s\n", ft.CreatedAt.Format(stamp))
	fmt.Printf("  prompt  %s\n", firstLine(ft.Prompt))
	fmt.Printf("  change  +%d -%d\n", ft.LinesAdded, ft.LinesRemoved)
}

// runStatus prints the database location and record counts.
func runStatus() {
	database := openDB()
	defer database.Close()

	sessions, turns, diffs, err := database.Counts()
	if err != nil {
		fatal("reading stats: %v", err)
	}
	fmt.Println("BlackBox")
	fmt.Printf("  database:  %s\n", database.Path())
	fmt.Printf("  sessions:  %d\n", sessions)
	fmt.Printf("  turns:     %d\n", turns)
	fmt.Printf("  diffs:     %d\n", diffs)
}

// --- shared helpers ------------------------------------------------------

func openDB() *db.DB {
	database, err := db.Open()
	if err != nil {
		fatal("could not open database: %v", err)
	}
	return database
}

func fatal(format string, a ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", a...)
	os.Exit(1)
}

func resolveSession(database *db.DB, idOrPrefix string) *db.Session {
	if s, err := database.GetSession(idOrPrefix); err == nil && s != nil {
		return s
	}
	ids, err := database.SessionIDsWithPrefix(idOrPrefix)
	if err != nil {
		fatal("resolving session: %v", err)
	}
	switch len(ids) {
	case 0:
		fatal("no session matches %q (see `blackbox log`)", idOrPrefix)
	case 1:
		s, err := database.GetSession(ids[0])
		if err != nil || s == nil {
			fatal("loading session %q: %v", ids[0], err)
		}
		return s
	default:
		fatal("%q is ambiguous — matches %d sessions; use more characters", idOrPrefix, len(ids))
	}
	return nil // unreachable
}

func resolveTurn(database *db.DB, idOrPrefix string) *db.Turn {
	if t, err := database.GetTurn(idOrPrefix); err == nil && t != nil {
		return t
	}
	ids, err := database.TurnIDsWithPrefix(idOrPrefix)
	if err != nil {
		fatal("resolving turn: %v", err)
	}
	switch len(ids) {
	case 0:
		fatal("no turn matches %q (see `blackbox show <session>`)", idOrPrefix)
	case 1:
		t, err := database.GetTurn(ids[0])
		if err != nil || t == nil {
			fatal("loading turn %q: %v", ids[0], err)
		}
		return t
	default:
		fatal("%q is ambiguous — matches %d turns; use more characters", idOrPrefix, len(ids))
	}
	return nil // unreachable
}

func printPrompt(prompt string) {
	lines := strings.Split(strings.TrimSpace(prompt), "\n")
	const max = 3
	for i, ln := range lines {
		if i == max {
			fmt.Printf("      > … (%d more line(s))\n", len(lines)-max)
			break
		}
		fmt.Printf("      > %s\n", ln)
	}
}

// parseFileLine splits "path/to/file:42" into its path and line number. The
// split is on the last colon so paths containing colons still parse.
func parseFileLine(target string) (string, int) {
	i := strings.LastIndex(target, ":")
	if i < 0 {
		return target, 0
	}
	if n, err := strconv.Atoi(target[i+1:]); err == nil && n > 0 {
		return target[:i], n
	}
	return target, 0
}

// repoRelative converts a user-supplied path into the repo-relative form stored
// in the database. If we're not in a git repo it returns the input unchanged.
func repoRelative(file string) string {
	wd, err := os.Getwd()
	if err != nil {
		return file
	}
	root, ok := git.Root(wd)
	if !ok {
		return file
	}
	abs := file
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(wd, file)
	}
	// Resolve symlinks on both sides so paths line up even when the repo lives
	// under a symlinked prefix (e.g. macOS /tmp -> /private/tmp).
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return file
	}
	return filepath.ToSlash(rel)
}

// patchCoversNewLine reports whether any hunk in patch adds or touches the given
// line number on the new side of the file.
func patchCoversNewLine(patch string, line int) bool {
	for _, ln := range strings.Split(patch, "\n") {
		if !strings.HasPrefix(ln, "@@") {
			continue
		}
		start, count, ok := newSideRange(ln)
		if ok && line >= start && line < start+count {
			return true
		}
	}
	return false
}

// newSideRange parses the "+start,count" span from a hunk header
// ("@@ -a,b +c,d @@").
func newSideRange(hunk string) (start, count int, ok bool) {
	for _, f := range strings.Fields(hunk) {
		if !strings.HasPrefix(f, "+") {
			continue
		}
		f = strings.TrimPrefix(f, "+")
		parts := strings.SplitN(f, ",", 2)
		start, err := strconv.Atoi(parts[0])
		if err != nil {
			return 0, 0, false
		}
		count = 1
		if len(parts) == 2 {
			if c, err := strconv.Atoi(parts[1]); err == nil {
				count = c
			}
		}
		return start, count, true
	}
	return 0, 0, false
}

func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	const max = 72
	if len(s) > max {
		return s[:max-1] + "…"
	}
	return s
}

func compactPath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && p != "" && strings.HasPrefix(p, home) {
		return "~" + p[len(home):]
	}
	return p
}
