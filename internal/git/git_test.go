package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// initRepo creates a throwaway git repo with one committed file.
func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "test@blackbox.local"},
		{"config", "user.name", "blackbox-test"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	writeFile(t, dir, "main.go", "package main\n\nfunc main() {}\n")
	commitAll(t, dir, "init")
	return dir
}

func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func commitAll(t *testing.T, dir, msg string) {
	t.Helper()
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-q", "-m", msg}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
}

func TestRootDetection(t *testing.T) {
	dir := initRepo(t)
	if root, ok := Root(dir); !ok || root == "" {
		t.Fatalf("expected repo root, got %q ok=%v", root, ok)
	}
	if _, ok := Root(os.TempDir()); ok {
		// TempDir itself should not be a repo (its per-test child is).
		t.Log("note: os.TempDir reported as a repo; skipping negative check")
	}
}

// TestSnapshotDiffReverse is the end-to-end contract BlackBox relies on:
// snapshot the tree, make a change, snapshot again, diff the two snapshots,
// then reverse-apply the diff to restore the original content.
func TestSnapshotDiffReverse(t *testing.T) {
	dir := initRepo(t)

	before, err := SnapshotTree(dir)
	if err != nil {
		t.Fatalf("baseline snapshot: %v", err)
	}

	// Simulate what an AI turn would do: edit a file and add a new one.
	writeFile(t, dir, "main.go", "package main\n\nfunc main() { println(\"hi\") }\n")
	writeFile(t, dir, "cache.go", "package main\n\nvar cache = map[string]string{}\n")

	after, err := SnapshotTree(dir)
	if err != nil {
		t.Fatalf("post-change snapshot: %v", err)
	}
	if before == after {
		t.Fatal("expected tree SHA to change after edits")
	}

	diffs, err := DiffTrees(dir, before, after)
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if len(diffs) != 2 {
		t.Fatalf("expected 2 changed files, got %d: %+v", len(diffs), diffs)
	}

	paths := map[string]FileDiff{}
	for _, d := range diffs {
		paths[d.Path] = d
	}
	if _, ok := paths["cache.go"]; !ok {
		t.Fatalf("expected cache.go in diff, got %v", diffs)
	}
	if paths["cache.go"].Added == 0 {
		t.Fatalf("expected added lines for new file, got %+v", paths["cache.go"])
	}

	// Reverse-apply every recorded patch; the working tree should return to
	// its baseline content.
	for _, d := range diffs {
		if err := ApplyReverse(dir, d.Patch); err != nil {
			t.Fatalf("reverse-apply %s: %v", d.Path, err)
		}
	}

	got, err := os.ReadFile(filepath.Join(dir, "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "package main\n\nfunc main() {}\n" {
		t.Fatalf("main.go not restored, got:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "cache.go")); !os.IsNotExist(err) {
		t.Fatalf("expected cache.go to be removed by reverse-apply, stat err=%v", err)
	}
}

func TestSnapshotLeavesIndexUntouched(t *testing.T) {
	dir := initRepo(t)

	// Stage a specific change, then snapshot; the staged state must survive.
	writeFile(t, dir, "main.go", "package main\n// staged\n")
	cmd := exec.Command("git", "add", "main.go")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v: %s", err, out)
	}

	statusBefore := gitStatus(t, dir)
	if _, err := SnapshotTree(dir); err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	statusAfter := gitStatus(t, dir)

	if statusBefore != statusAfter {
		t.Fatalf("snapshot disturbed the index:\nbefore:\n%s\nafter:\n%s", statusBefore, statusAfter)
	}
}

func gitStatus(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git status: %v: %s", err, out)
	}
	return string(out)
}
