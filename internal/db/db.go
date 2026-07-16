// Package db owns BlackBox's local SQLite store: the sessions, turns, and
// diffs that make up a prompt-by-prompt audit trail of an AI coding session.
package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const schema = `
CREATE TABLE IF NOT EXISTS sessions (
    session_id TEXT PRIMARY KEY,
    repo_path  TEXT,
    started_at INTEGER
);

CREATE TABLE IF NOT EXISTS turns (
    turn_id          TEXT PRIMARY KEY,
    session_id       TEXT,
    prompt           TEXT,
    turn_number      INTEGER,
    time_prompt_made INTEGER,
    baseline_tree    TEXT,
    ended_at         INTEGER,
    FOREIGN KEY (session_id) REFERENCES sessions(session_id)
);

CREATE TABLE IF NOT EXISTS diff (
    diff_id       TEXT PRIMARY KEY,
    turn_id       TEXT,
    file_path     TEXT,
    changes_made  TEXT,
    lines_added   INTEGER,
    lines_removed INTEGER,
    FOREIGN KEY (turn_id) REFERENCES turns(turn_id)
);

CREATE INDEX IF NOT EXISTS idx_turns_session ON turns(session_id);
CREATE INDEX IF NOT EXISTS idx_diff_turn ON diff(turn_id);
CREATE INDEX IF NOT EXISTS idx_diff_file ON diff(file_path);
`

// Session is one Claude Code session, scoped to a repo.
type Session struct {
	SessionID string
	RepoPath  string
	StartedAt time.Time
	TurnCount int // populated by list queries only
}

// Turn is a single prompt within a session and the diff it produced.
type Turn struct {
	TurnID       string
	SessionID    string
	Prompt       string
	TurnNumber   int
	CreatedAt    time.Time
	BaselineTree string     // git tree SHA captured when the prompt was submitted
	EndedAt      *time.Time // nil while the turn is still open (agent still working)
}

// Diff is a per-file unified patch attributed to a turn.
type Diff struct {
	DiffID       string
	TurnID       string
	FilePath     string
	Patch        string // stored in changes_made; a `git apply`-able unified diff
	LinesAdded   int
	LinesRemoved int
}

// FileTouch is a diff plus the prompt/turn that produced it, used by `blame`.
type FileTouch struct {
	Diff
	Prompt     string
	SessionID  string
	TurnNumber int
	CreatedAt  time.Time
}

// DB wraps the SQLite connection and knows its own on-disk path.
type DB struct {
	conn *sql.DB
	path string
}

// Open finds (or creates) ~/.blackbox/sessions.db, applies migrations, and
// returns a ready-to-use handle.
func Open() (*DB, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	dbDir := filepath.Join(homeDir, ".blackbox")
	dbPath := filepath.Join(dbDir, "sessions.db")

	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		return nil, err
	}

	conn, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}
	if err := conn.Ping(); err != nil {
		return nil, err
	}
	// Hooks fire as short-lived processes that may briefly overlap; wait for
	// the lock instead of failing, and enforce foreign keys.
	if _, err := conn.Exec("PRAGMA busy_timeout = 5000"); err != nil {
		return nil, err
	}
	if _, err := conn.Exec("PRAGMA foreign_keys = ON"); err != nil {
		return nil, err
	}

	d := &DB{conn: conn, path: dbPath}
	if err := d.migrate(); err != nil {
		return nil, err
	}
	return d, nil
}

// Path returns the database file location (used by `blackbox status`).
func (d *DB) Path() string { return d.path }

// Close releases the underlying connection.
func (d *DB) Close() error { return d.conn.Close() }

func (d *DB) migrate() error {
	if _, err := d.conn.Exec(schema); err != nil {
		return err
	}
	// Forward-compat: older dev databases predate these columns.
	for col, def := range map[string]string{
		"baseline_tree": "TEXT",
		"ended_at":      "INTEGER",
	} {
		if err := d.addColumnIfMissing("turns", col, def); err != nil {
			return err
		}
	}
	return nil
}

func (d *DB) addColumnIfMissing(table, column, def string) error {
	rows, err := d.conn.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			cid, notnull, pk int
			name, ctype      string
			dfltValue        sql.NullString
		)
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dfltValue, &pk); err != nil {
			return err
		}
		if name == column {
			return rows.Err() // already present
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = d.conn.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column, def))
	return err
}

// InsertSession records a session the first time we see it; repeat calls for
// the same id are no-ops.
func (d *DB) InsertSession(sessionID, repoPath string) error {
	_, err := d.conn.Exec(
		`INSERT INTO sessions (session_id, repo_path, started_at)
		 VALUES (?, ?, ?)
		 ON CONFLICT (session_id) DO NOTHING`,
		sessionID, repoPath, time.Now().Unix())
	return err
}

// NextTurnNumber returns the 1-based position of the next turn in a session.
func (d *DB) NextTurnNumber(sessionID string) (int, error) {
	var n int
	err := d.conn.QueryRow(
		`SELECT COUNT(*) FROM turns WHERE session_id = ?`, sessionID).Scan(&n)
	if err != nil {
		return 0, err
	}
	return n + 1, nil
}

// InsertTurn records a new prompt along with the baseline tree snapshot taken
// when it was submitted. The turn is left "open" (ended_at NULL) until the
// agent stops.
func (d *DB) InsertTurn(turnID, sessionID, prompt string, turnNumber int, baselineTree string) error {
	_, err := d.conn.Exec(
		`INSERT INTO turns (turn_id, session_id, prompt, turn_number, time_prompt_made, baseline_tree)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT (turn_id) DO NOTHING`,
		turnID, sessionID, prompt, turnNumber, time.Now().Unix(), baselineTree)
	return err
}

// OpenTurn returns the most recent still-open turn for a session, or nil if
// there is none.
func (d *DB) OpenTurn(sessionID string) (*Turn, error) {
	row := d.conn.QueryRow(
		`SELECT turn_id, session_id, prompt, turn_number, time_prompt_made, baseline_tree
		 FROM turns
		 WHERE session_id = ? AND ended_at IS NULL
		 ORDER BY turn_number DESC
		 LIMIT 1`, sessionID)

	var (
		t         Turn
		createdAt int64
		baseline  sql.NullString
	)
	err := row.Scan(&t.TurnID, &t.SessionID, &t.Prompt, &t.TurnNumber, &createdAt, &baseline)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	t.CreatedAt = time.Unix(createdAt, 0)
	t.BaselineTree = baseline.String
	return &t, nil
}

// FinalizeTurn marks a turn as complete.
func (d *DB) FinalizeTurn(turnID string) error {
	_, err := d.conn.Exec(
		`UPDATE turns SET ended_at = ? WHERE turn_id = ?`, time.Now().Unix(), turnID)
	return err
}

// InsertDiff attaches a per-file patch to a turn.
func (d *DB) InsertDiff(diffID, turnID, filePath, patch string, linesAdded, linesRemoved int) error {
	_, err := d.conn.Exec(
		`INSERT INTO diff (diff_id, turn_id, file_path, changes_made, lines_added, lines_removed)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT (diff_id) DO NOTHING`,
		diffID, turnID, filePath, patch, linesAdded, linesRemoved)
	return err
}

// ListSessions returns every session, newest first, with its turn count.
func (d *DB) ListSessions() ([]Session, error) {
	rows, err := d.conn.Query(
		`SELECT s.session_id, s.repo_path, s.started_at, COUNT(t.turn_id)
		 FROM sessions s
		 LEFT JOIN turns t ON t.session_id = s.session_id
		 GROUP BY s.session_id
		 ORDER BY s.started_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Session
	for rows.Next() {
		var (
			s         Session
			startedAt int64
		)
		if err := rows.Scan(&s.SessionID, &s.RepoPath, &startedAt, &s.TurnCount); err != nil {
			return nil, err
		}
		s.StartedAt = time.Unix(startedAt, 0)
		out = append(out, s)
	}
	return out, rows.Err()
}

// GetSession returns a single session by id.
func (d *DB) GetSession(sessionID string) (*Session, error) {
	row := d.conn.QueryRow(
		`SELECT session_id, repo_path, started_at FROM sessions WHERE session_id = ?`, sessionID)
	var (
		s         Session
		startedAt int64
	)
	err := row.Scan(&s.SessionID, &s.RepoPath, &startedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	s.StartedAt = time.Unix(startedAt, 0)
	return &s, nil
}

// GetTurns returns all turns for a session in chronological order.
func (d *DB) GetTurns(sessionID string) ([]Turn, error) {
	rows, err := d.conn.Query(
		`SELECT turn_id, session_id, prompt, turn_number, time_prompt_made, baseline_tree, ended_at
		 FROM turns
		 WHERE session_id = ?
		 ORDER BY turn_number ASC`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTurns(rows)
}

// GetTurn returns a single turn by id.
func (d *DB) GetTurn(turnID string) (*Turn, error) {
	rows, err := d.conn.Query(
		`SELECT turn_id, session_id, prompt, turn_number, time_prompt_made, baseline_tree, ended_at
		 FROM turns WHERE turn_id = ?`, turnID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	turns, err := scanTurns(rows)
	if err != nil {
		return nil, err
	}
	if len(turns) == 0 {
		return nil, nil
	}
	return &turns[0], nil
}

func scanTurns(rows *sql.Rows) ([]Turn, error) {
	var out []Turn
	for rows.Next() {
		var (
			t         Turn
			createdAt int64
			baseline  sql.NullString
			endedAt   sql.NullInt64
		)
		if err := rows.Scan(&t.TurnID, &t.SessionID, &t.Prompt, &t.TurnNumber, &createdAt, &baseline, &endedAt); err != nil {
			return nil, err
		}
		t.CreatedAt = time.Unix(createdAt, 0)
		t.BaselineTree = baseline.String
		if endedAt.Valid {
			ts := time.Unix(endedAt.Int64, 0)
			t.EndedAt = &ts
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// GetDiffsForTurn returns all per-file diffs recorded for a turn.
func (d *DB) GetDiffsForTurn(turnID string) ([]Diff, error) {
	rows, err := d.conn.Query(
		`SELECT diff_id, turn_id, file_path, changes_made, lines_added, lines_removed
		 FROM diff WHERE turn_id = ? ORDER BY file_path ASC`, turnID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Diff
	for rows.Next() {
		var df Diff
		if err := rows.Scan(&df.DiffID, &df.TurnID, &df.FilePath, &df.Patch, &df.LinesAdded, &df.LinesRemoved); err != nil {
			return nil, err
		}
		out = append(out, df)
	}
	return out, rows.Err()
}

// FileHistory returns every diff that touched a file, newest first, joined with
// the prompt that produced it. Powers `blackbox blame`.
func (d *DB) FileHistory(filePath string) ([]FileTouch, error) {
	rows, err := d.conn.Query(
		`SELECT d.diff_id, d.turn_id, d.file_path, d.changes_made, d.lines_added, d.lines_removed,
		        t.prompt, t.session_id, t.turn_number, t.time_prompt_made
		 FROM diff d
		 JOIN turns t ON t.turn_id = d.turn_id
		 WHERE d.file_path = ?
		 ORDER BY t.time_prompt_made DESC`, filePath)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []FileTouch
	for rows.Next() {
		var (
			ft        FileTouch
			createdAt int64
		)
		if err := rows.Scan(&ft.DiffID, &ft.TurnID, &ft.FilePath, &ft.Patch, &ft.LinesAdded, &ft.LinesRemoved,
			&ft.Prompt, &ft.SessionID, &ft.TurnNumber, &createdAt); err != nil {
			return nil, err
		}
		ft.CreatedAt = time.Unix(createdAt, 0)
		out = append(out, ft)
	}
	return out, rows.Err()
}

// Counts returns the number of sessions, turns, and diffs on record.
func (d *DB) Counts() (sessions, turns, diffs int, err error) {
	if err = d.conn.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&sessions); err != nil {
		return
	}
	if err = d.conn.QueryRow(`SELECT COUNT(*) FROM turns`).Scan(&turns); err != nil {
		return
	}
	err = d.conn.QueryRow(`SELECT COUNT(*) FROM diff`).Scan(&diffs)
	return
}
