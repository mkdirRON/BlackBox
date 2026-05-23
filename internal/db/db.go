package db

import ( 
	"os"
	"fmt"
	"database/sql"
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


type DB struct { 
	conn *sql.DB
}
// entry point for find new/exisiting DB's 
// Find or create the ~/.blackbox/ directory using os.UserHomeDir() 
//Open the database file at ~/.blackbox/sessions.db
// Return a *DB and any error
func Open() (*DB, error) { 
	homeDir, err := os.UserHomeDir()

	if err != nil { 
		return nil, err 
	}
  dbPath := fmt.Sprintf("%s/.blackbox/sessions.db", homeDir)
	dbDir := fmt.Sprintf("%s/.blackbox", homeDir)

	mkdirErr := os.MkdirAll(dbDir, 0755)
	if mkdirErr != nil { 
		return nil, mkdirErr
	}

	db, sqlConnErr := sql.Open("sqlite", dbPath)
	if sqlConnErr != nil{ 
		return nil, sqlConnErr 
	}

	dbErr := db.Ping()
	if dbErr != nil { 
		return nil, dbErr
	}
	dbConn := &DB { 
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

_,dbErr := d.conn.Exec(query)

if dbErr != nil {
	return dbErr
}

return nil 
}
