package db

import (
	"database/sql"
	"fmt"
	"os"
	"time"
)

const query = `CREATE TABLE IF NOT EXISTS sessions (
    session_id TEXT PRIMARY KEY,
    repo_path TEXT,
    started_at INTEGER
);

CREATE TABLE IF NOT EXISTS turns (
    turn_id TEXT PRIMARY KEY,
    session_id TEXT,
    prompt TEXT,
    turn_number INTEGER,
    time_prompt_made INTEGER,
    FOREIGN KEY (session_id) REFERENCES sessions(session_id)
);

CREATE TABLE IF NOT EXISTS diff (
    diff_id TEXT PRIMARY KEY,
    turn_id TEXT,
    file_path TEXT,
    changes_made TEXT,
    lines_added INTEGER,
    lines_removed INTEGER,
    FOREIGN KEY (turn_id) REFERENCES turns(turn_id)
);`

type Session struct {
	SessionID string
	RepoPath  string
	StartedAt time.Time
}

type DB struct {
	conn *sql.DB
}

// entry point for find new/exisiting DB's
// Find or create the ~/.blackbox/ directory using os.UserHomeDir()
// Open the database file at ~/.blackbox/sessions.db
// Return a *DB and any error
func Open() (*DB, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	dbPath := fmt.Sprintf("%s/.blackbox/sessions.db", homeDir)
	dbDir := fmt.Sprintf("%s/.blackbox", homeDir)

	mkdirErr := os.MkdirAll(dbDir, 0o755)
	if mkdirErr != nil {
		return nil, mkdirErr
	}

	db, sqlConnErr := sql.Open("sqlite", dbPath)
	if sqlConnErr != nil {
		return nil, sqlConnErr
	}

	dbErr := db.Ping()
	if dbErr != nil {
		return nil, dbErr
	}
	dbConn := &DB{
		conn: db,
	}
	_, err = db.Exec("PRAGMA foreign_keys = ON")
	if err != nil {
		return nil, err
	}
	migrationErr := dbConn.migrate()
	if migrationErr != nil {
		return nil, migrationErr
	}

	return dbConn, nil
}

func (d *DB) migrate() error {
	_, dbErr := d.conn.Exec(query)

	if dbErr != nil {
		return dbErr
	}

	return nil
}

func (d *DB) InsertSession(sessionID string, repoPath string) error {
	_, err := d.conn.Exec("INSERT INTO sessions (session_id, repo_path, started_at) VALUES(?,?,?) ON CONFLICT (session_id) DO NOTHING", sessionID, repoPath, time.Now().Unix())
	if err != nil {
		return err
	}
	return nil
}

func (d *DB) GetSessions() ([]Session, error) {
	var res []Session

	qry, err := d.conn.Query(`SELECT session_id, repo_path, started_at 
											FROM sessions
											ORDER BY started_at DESC`)
	if err != nil {
		return res, err
	}

	defer qry.Close()
	for qry.Next() {
		var s Session
		var startedAtUnix int64
		err := qry.Scan(
			&s.SessionID,
			&s.RepoPath,
			&startedAtUnix,
		)
		if err != nil {
			return nil, err
		}
		s.StartedAt = time.Unix(startedAtUnix, 0)
		res = append(res, s)
	}
	if err := qry.Err(); err != nil {
		return nil, err
	}
	return res, nil
}

func (d *DB) InsertDiff(diffID string, turnID string, filePath string, changesMade string, linesAdded int, linesRemoved int) error {
	_, err := d.conn.Exec(`INSERT INTO diff (diff_id, turn_id, file_path, changes_made, lines_added, lines_removed)	
																								VALUES (?,?,?,?,?,?) ON CONFLICT(diff_id) DO NOTHING`,
		diffID, turnID, filePath, changesMade, linesAdded, linesRemoved)
	if err != nil {
		return err
	}
	return nil
}

func (d *DB) InsertTurn(turnID string, sessionID string, prompt string, turnNumber int) error {
	_, err := d.conn.Exec(`INSERT INTO turns (turn_id, sessionID, prompt, turn_number, time_prompt_made)
		 																							 VALUES (?,?,?,?,?) ON CONFLICT(turn_id) DO NOTHING`, turnID, sessionID, prompt, turnNumber, time.Now().Unix())
	if err != nil {
		return err
	}
	return nil
}

// CREATE TABLE IF NOT EXISTS turns (
//     turn_id TEXT PRIMARY KEY,
//     session_id TEXT,
//     prompt TEXT,
//     turn_number INTEGER,
//     time_prompt_made INTEGER,
//     FOREIGN KEY (session_id) REFERENCES sessions(session_id)
// );
//
// CREATE TABLE IF NOT EXISTS diff (
//     diff_id TEXT PRIMARY KEY,
//     turn_id TEXT,
//     file_path TEXT,
//     changes_made TEXT,
//     lines_added INTEGER,
//     lines_removed INTEGER,
//     FOREIGN KEY (turn_id) REFERENCES turns(turn_id)
// );`
//
