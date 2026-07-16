package hooks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestInstallCreatesHooks(t *testing.T) {
	dir := t.TempDir()

	rep, err := Install(dir, "/usr/local/bin/blackbox")
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Added) != len(Events) {
		t.Fatalf("expected %d events added, got %v", len(Events), rep.Added)
	}

	root := readJSON(t, rep.SettingsPath)
	hooks, ok := root["hooks"].(map[string]any)
	if !ok {
		t.Fatalf("hooks key missing or wrong type: %v", root)
	}
	for _, event := range Events {
		if _, ok := hooks[event]; !ok {
			t.Fatalf("event %s not installed", event)
		}
	}
}

func TestInstallIsIdempotent(t *testing.T) {
	dir := t.TempDir()

	if _, err := Install(dir, "/usr/local/bin/blackbox"); err != nil {
		t.Fatal(err)
	}
	rep, err := Install(dir, "/usr/local/bin/blackbox")
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Added) != 0 {
		t.Fatalf("second install should add nothing, added %v", rep.Added)
	}
	if len(rep.Existing) != len(Events) {
		t.Fatalf("expected all events reported existing, got %v", rep.Existing)
	}
}

func TestInstallPreservesExistingSettings(t *testing.T) {
	dir := t.TempDir()
	claudeDir := filepath.Join(dir, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	existing := `{
  "permissions": { "allow": ["Bash(go build *)"] },
  "hooks": {
    "UserPromptSubmit": [
      { "hooks": [ { "type": "command", "command": "some-other-tool" } ] }
    ]
  }
}`
	settingsPath := filepath.Join(claudeDir, "settings.json")
	if err := os.WriteFile(settingsPath, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Install(dir, "/usr/local/bin/blackbox"); err != nil {
		t.Fatal(err)
	}

	root := readJSON(t, settingsPath)
	if _, ok := root["permissions"]; !ok {
		t.Fatal("unrelated 'permissions' block was dropped")
	}
	hooks := root["hooks"].(map[string]any)
	groups := hooks["UserPromptSubmit"].([]any)
	if len(groups) != 2 {
		t.Fatalf("expected existing hook preserved alongside BlackBox's, got %d groups", len(groups))
	}
	if !hasCommand(groups, `"/usr/local/bin/blackbox" hook`) {
		t.Fatalf("BlackBox hook not present after merge: %v", groups)
	}
}

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, data)
	}
	return root
}
