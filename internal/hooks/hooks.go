// Package hooks installs BlackBox's capture hooks into a project's Claude Code
// settings file, merging into any configuration that is already there.
package hooks

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Events are the Claude Code hooks BlackBox registers. UserPromptSubmit opens a
// turn; Stop closes it and records the diff.
var Events = []string{"UserPromptSubmit", "Stop"}

// subcommand is the argument the hooks pass back to the BlackBox binary.
const subcommand = "hook"

// Report describes what Install changed.
type Report struct {
	SettingsPath string
	Command      string
	Added        []string // events newly registered
	Existing     []string // events already registered
}

// Install ensures every entry in Events is present in
// <projectDir>/.claude/settings.json, invoking exePath. Existing settings and
// unrelated hooks are preserved; re-running is a no-op.
func Install(projectDir, exePath string) (Report, error) {
	claudeDir := filepath.Join(projectDir, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		return Report{}, err
	}
	settingsPath := filepath.Join(claudeDir, "settings.json")

	root, err := readSettings(settingsPath)
	if err != nil {
		return Report{}, err
	}

	hooksMap, _ := root["hooks"].(map[string]any)
	if hooksMap == nil {
		hooksMap = map[string]any{}
	}

	command := fmt.Sprintf("%q %s", exePath, subcommand)
	rep := Report{SettingsPath: settingsPath, Command: command}

	for _, event := range Events {
		groups, _ := hooksMap[event].([]any)
		if hasCommand(groups, command) {
			rep.Existing = append(rep.Existing, event)
			continue
		}
		groups = append(groups, map[string]any{
			"hooks": []any{
				map[string]any{"type": "command", "command": command},
			},
		})
		hooksMap[event] = groups
		rep.Added = append(rep.Added, event)
	}
	root["hooks"] = hooksMap

	if err := writeSettings(settingsPath, root); err != nil {
		return Report{}, err
	}
	return rep, nil
}

func readSettings(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}, nil
		}
		return nil, err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return map[string]any{}, nil
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("%s is not valid JSON: %w", path, err)
	}
	if root == nil {
		root = map[string]any{}
	}
	return root, nil
}

func writeSettings(path string, root map[string]any) error {
	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(out, '\n'), 0o644)
}

// hasCommand reports whether any hook group already runs command.
func hasCommand(groups []any, command string) bool {
	for _, g := range groups {
		gm, ok := g.(map[string]any)
		if !ok {
			continue
		}
		inner, ok := gm["hooks"].([]any)
		if !ok {
			continue
		}
		for _, h := range inner {
			hm, ok := h.(map[string]any)
			if !ok {
				continue
			}
			if s, ok := hm["command"].(string); ok && s == command {
				return true
			}
		}
	}
	return false
}
