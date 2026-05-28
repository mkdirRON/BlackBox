package capture

type UserPromptSubmit struct {
	sessionID     string `json:"session_id"`
	hookEventName string `json:"hook_event_name"`
	cwd           string `json:"cwd"`
	prompt        string `json:"prompt"`
}

type PostToolUse struct {
	sessionID     string `json:"session_id"`
	hookEventName string `json:"hook_event_name"`
	cwd           string `json:"cwd"`
	toolName      string `json:"tool_name"`
	toolpath             // nested

}
