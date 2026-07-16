// Package ui serves BlackBox's local session-timeline web interface: a
// read-only view of every session, its prompts, and the diff each prompt
// produced.
package ui

import (
	"fmt"
	"html/template"
	"net/http"
	"strings"

	"github.com/mkdirRON/BlackBox/internal/db"
)

const timeFormat = "2006-01-02 15:04:05"

// Serve starts the timeline web server and blocks until it exits.
func Serve(database *db.DB, addr string) error {
	tmpl, err := template.New("ui").Parse(pageTemplate)
	if err != nil {
		return err
	}
	srv := &server{db: database, tmpl: tmpl}

	mux := http.NewServeMux()
	mux.HandleFunc("/", srv.handleIndex)
	mux.HandleFunc("/session", srv.handleSession)

	fmt.Printf("BlackBox timeline: http://%s\n", addr)
	return http.ListenAndServe(addr, mux)
}

type server struct {
	db   *db.DB
	tmpl *template.Template
}

// --- view models ---------------------------------------------------------

type pageData struct {
	Title    string
	Sessions []sessionRow
	Session  *sessionDetail
}

type sessionRow struct {
	ID      string
	ShortID string
	Repo    string
	When    string
	Turns   int
}

type sessionDetail struct {
	ShortID string
	Repo    string
	When    string
	Turns   []turnView
}

type turnView struct {
	Number int
	Prompt string
	When   string
	Diffs  []diffView
}

type diffView struct {
	Path    string
	Added   int
	Removed int
	Lines   []diffLine
}

type diffLine struct {
	Class string
	Text  string
}

// --- handlers ------------------------------------------------------------

func (s *server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	sessions, err := s.db.ListSessions()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	rows := make([]sessionRow, 0, len(sessions))
	for _, sess := range sessions {
		rows = append(rows, sessionRow{
			ID:      sess.SessionID,
			ShortID: shortID(sess.SessionID),
			Repo:    sess.RepoPath,
			When:    sess.StartedAt.Format(timeFormat),
			Turns:   sess.TurnCount,
		})
	}
	s.render(w, pageData{Title: "Sessions", Sessions: rows})
}

func (s *server) handleSession(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		http.Error(w, "missing session id", http.StatusBadRequest)
		return
	}
	sess, err := s.db.GetSession(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if sess == nil {
		http.NotFound(w, r)
		return
	}
	turns, err := s.db.GetTurns(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	detail := &sessionDetail{
		ShortID: shortID(sess.SessionID),
		Repo:    sess.RepoPath,
		When:    sess.StartedAt.Format(timeFormat),
	}
	for _, t := range turns {
		tv := turnView{Number: t.TurnNumber, Prompt: t.Prompt, When: t.CreatedAt.Format(timeFormat)}
		diffs, err := s.db.GetDiffsForTurn(t.TurnID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		for _, d := range diffs {
			tv.Diffs = append(tv.Diffs, diffView{
				Path:    d.FilePath,
				Added:   d.LinesAdded,
				Removed: d.LinesRemoved,
				Lines:   classifyPatch(d.Patch),
			})
		}
		detail.Turns = append(detail.Turns, tv)
	}
	s.render(w, pageData{Title: "Session " + detail.ShortID, Session: detail})
}

func (s *server) render(w http.ResponseWriter, data pageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.Execute(w, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// --- helpers -------------------------------------------------------------

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// classifyPatch tags each line of a unified diff so the template can colour it.
func classifyPatch(patch string) []diffLine {
	lines := strings.Split(strings.TrimRight(patch, "\n"), "\n")
	out := make([]diffLine, 0, len(lines))
	for _, ln := range lines {
		out = append(out, diffLine{Class: lineClass(ln), Text: ln})
	}
	return out
}

func lineClass(ln string) string {
	switch {
	case strings.HasPrefix(ln, "@@"):
		return "hunk"
	case strings.HasPrefix(ln, "+++"), strings.HasPrefix(ln, "---"),
		strings.HasPrefix(ln, "diff "), strings.HasPrefix(ln, "index "),
		strings.HasPrefix(ln, "new file"), strings.HasPrefix(ln, "deleted file"):
		return "meta"
	case strings.HasPrefix(ln, "+"):
		return "add"
	case strings.HasPrefix(ln, "-"):
		return "del"
	default:
		return "ctx"
	}
}

const pageTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>BlackBox — {{.Title}}</title>
<style>
  :root { color-scheme: dark; }
  * { box-sizing: border-box; }
  body { margin: 0; background: #0d1117; color: #c9d1d9;
         font: 14px/1.5 -apple-system, BlinkMacSystemFont, "Segoe UI", Helvetica, Arial, sans-serif; }
  a { color: #58a6ff; text-decoration: none; }
  a:hover { text-decoration: underline; }
  header { padding: 20px 32px; border-bottom: 1px solid #21262d; background: #161b22; }
  header h1 { margin: 0; font-size: 18px; }
  header .brand { color: #8b949e; font-weight: normal; }
  main { max-width: 960px; margin: 0 auto; padding: 24px 32px 64px; }
  .muted { color: #8b949e; }
  table { width: 100%; border-collapse: collapse; }
  th, td { text-align: left; padding: 10px 12px; border-bottom: 1px solid #21262d; }
  th { color: #8b949e; font-weight: 600; font-size: 12px; text-transform: uppercase; letter-spacing: .04em; }
  tr:hover td { background: #161b22; }
  .pill { display: inline-block; min-width: 22px; text-align: center; padding: 1px 8px;
          border-radius: 999px; background: #21262d; color: #c9d1d9; font-size: 12px; }
  .turn { border: 1px solid #21262d; border-radius: 8px; margin: 18px 0; overflow: hidden; }
  .turn-head { display: flex; align-items: baseline; gap: 10px; padding: 12px 16px; background: #161b22; }
  .turn-num { font-weight: 700; color: #58a6ff; }
  .prompt { white-space: pre-wrap; padding: 14px 16px; border-top: 1px solid #21262d;
            font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 13px; }
  .file { border-top: 1px solid #21262d; }
  .file-head { padding: 8px 16px; background: #0d1117; font-family: ui-monospace, monospace; font-size: 13px; }
  .add-count { color: #3fb950; } .del-count { color: #f85149; }
  pre.diff { margin: 0; padding: 8px 0; overflow-x: auto; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 12.5px; }
  pre.diff .line { display: block; padding: 0 16px; white-space: pre; }
  .add { background: rgba(63,185,80,.15); color: #56d364; }
  .del { background: rgba(248,81,73,.15); color: #f85149; }
  .hunk { color: #a5a5ff; }
  .meta { color: #8b949e; }
  .empty { padding: 10px 16px; color: #8b949e; font-style: italic; border-top: 1px solid #21262d; }
</style>
</head>
<body>
<header><h1><span class="brand">BlackBox /</span> {{.Title}}</h1></header>
<main>
{{if .Session}}
  {{with .Session}}
  <p class="muted"><a href="/">← all sessions</a></p>
  <p class="muted">{{.Repo}} · started {{.When}}</p>
  {{range .Turns}}
    <section class="turn">
      <div class="turn-head"><span class="turn-num">#{{.Number}}</span><span class="muted">{{.When}}</span></div>
      <div class="prompt">{{.Prompt}}</div>
      {{if .Diffs}}
        {{range .Diffs}}
        <div class="file">
          <div class="file-head">{{.Path}}
            <span class="add-count">+{{.Added}}</span> <span class="del-count">−{{.Removed}}</span>
          </div>
          <pre class="diff">{{range .Lines}}<span class="line {{.Class}}">{{.Text}}</span>{{end}}</pre>
        </div>
        {{end}}
      {{else}}
        <div class="empty">No file changes for this prompt.</div>
      {{end}}
    </section>
  {{end}}
  {{end}}
{{else}}
  {{if .Sessions}}
  <table>
    <thead><tr><th>Session</th><th>Repo</th><th>Started</th><th>Turns</th></tr></thead>
    <tbody>
    {{range .Sessions}}
      <tr>
        <td><a href="/session?id={{.ID}}">{{.ShortID}}</a></td>
        <td class="muted">{{.Repo}}</td>
        <td class="muted">{{.When}}</td>
        <td><span class="pill">{{.Turns}}</span></td>
      </tr>
    {{end}}
    </tbody>
  </table>
  {{else}}
  <p class="muted">No sessions recorded yet. Run <code>blackbox init</code> in a repo, then start coding with Claude Code.</p>
  {{end}}
{{end}}
</main>
</body>
</html>`
