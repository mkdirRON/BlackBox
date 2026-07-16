// Package git wraps the handful of git plumbing commands BlackBox needs to
// snapshot a working tree, diff two snapshots, and reverse-apply a stored patch.
//
// Snapshots are taken with a throwaway index (GIT_INDEX_FILE) so that capturing
// state never disturbs what the developer has staged. The result is a tree SHA
// that we can diff against a later snapshot to get exactly what a prompt changed.
package git

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// FileDiff is a single file's unified patch plus its line counts.
type FileDiff struct {
	Path    string
	Patch   string
	Added   int
	Removed int
}

// run executes git in root, optionally with extra env and stdin, returning
// stdout. Stderr is folded into the error for readable diagnostics.
func run(root string, env []string, stdin string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	if env != nil {
		cmd.Env = env
	}
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errBuf.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return out.String(), nil
}

// Root returns the repository root containing dir, and whether dir is inside a
// git working tree at all.
func Root(dir string) (string, bool) {
	out, err := run(dir, nil, "", "rev-parse", "--show-toplevel")
	if err != nil {
		return "", false
	}
	root := strings.TrimSpace(out)
	return root, root != ""
}

// SnapshotTree records the current state of the working tree as a git tree
// object and returns its SHA. It uses a temporary index seeded from the real
// one (for speed) so the developer's staging area is left untouched.
func SnapshotTree(root string) (string, error) {
	tmp, err := os.CreateTemp("", "blackbox-index-*")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	tmp.Close()
	os.Remove(tmpPath) // git will (re)create it; seed below if possible
	defer os.Remove(tmpPath)

	seedTempIndex(root, tmpPath)

	env := append(os.Environ(), "GIT_INDEX_FILE="+tmpPath)
	if _, err := run(root, env, "", "add", "-A"); err != nil {
		return "", err
	}
	out, err := run(root, env, "", "write-tree")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// seedTempIndex copies the repo's real index into tmpPath so `git add -A` only
// rehashes changed files. Best effort: on any failure we start from an empty
// index, which is slower but equally correct.
func seedTempIndex(root, tmpPath string) {
	out, err := run(root, nil, "", "rev-parse", "--git-path", "index")
	if err != nil {
		return
	}
	realIdx := strings.TrimSpace(out)
	if !filepath.IsAbs(realIdx) {
		realIdx = filepath.Join(root, realIdx)
	}
	data, err := os.ReadFile(realIdx)
	if err != nil {
		return
	}
	_ = os.WriteFile(tmpPath, data, 0o600)
}

// DiffTrees returns the per-file unified diffs that turn treeA into treeB.
// Rename detection is disabled so every change is expressed as concrete
// add/modify/delete hunks that reverse-apply predictably.
func DiffTrees(root, treeA, treeB string) ([]FileDiff, error) {
	out, err := run(root, nil, "",
		"diff", "--no-color", "--no-ext-diff", "--no-renames", "--src-prefix=a/", "--dst-prefix=b/",
		treeA, treeB)
	if err != nil {
		return nil, err
	}
	return parseUnifiedDiff(out), nil
}

// ApplyReverse undoes a stored patch against the working tree. It dry-runs with
// --check first so a conflicting revert fails loudly instead of half-applying.
func ApplyReverse(root, patch string) error {
	if _, err := run(root, nil, patch, "apply", "--reverse", "--check"); err != nil {
		return fmt.Errorf("patch will not reverse cleanly (the code has changed since it was recorded): %w", err)
	}
	if _, err := run(root, nil, patch, "apply", "--reverse"); err != nil {
		return err
	}
	return nil
}

// parseUnifiedDiff splits a combined `git diff` into one FileDiff per file and
// tallies added/removed lines.
func parseUnifiedDiff(patch string) []FileDiff {
	var (
		files  []FileDiff
		cur    *FileDiff
		header string
		buf    []string
	)

	commit := func() {
		if cur == nil {
			return
		}
		if cur.Path == "" {
			cur.Path = pathFromHeader(header)
		}
		body := strings.Join(buf, "\n")
		if !strings.HasSuffix(body, "\n") {
			body += "\n"
		}
		cur.Patch = body
		files = append(files, *cur)
		cur, buf = nil, nil
	}

	for _, ln := range strings.Split(patch, "\n") {
		if strings.HasPrefix(ln, "diff --git ") {
			commit()
			cur = &FileDiff{}
			header = ln
		}
		if cur == nil {
			continue
		}
		buf = append(buf, ln)
		switch {
		case strings.HasPrefix(ln, "+++ b/"):
			cur.Path = strings.TrimPrefix(ln, "+++ b/")
		case strings.HasPrefix(ln, "--- a/") && cur.Path == "":
			cur.Path = strings.TrimPrefix(ln, "--- a/")
		case len(ln) > 0 && ln[0] == '+' && !strings.HasPrefix(ln, "+++"):
			cur.Added++
		case len(ln) > 0 && ln[0] == '-' && !strings.HasPrefix(ln, "---"):
			cur.Removed++
		}
	}
	commit()
	return files
}

// pathFromHeader recovers a file path from a `diff --git a/x b/x` line for the
// binary-file case, where no +++/--- lines are emitted.
func pathFromHeader(header string) string {
	rest := strings.TrimPrefix(header, "diff --git ")
	if i := strings.Index(rest, " b/"); i >= 0 {
		return rest[i+3:]
	}
	return strings.TrimSpace(rest)
}
