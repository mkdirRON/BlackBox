package db

import ( 
	"os"
	"fmt"
	"database/sql"
)

type DB struct { 
	conn *sql.DB
}
// entry point for find new/exisiting DB's 
// Find or create the ~/.blackbox/ directory using os.UserHomeDir() 
//Open the database file at ~/.blackbox/sessions.db
// Return a *DB and any error
func Open() (*DB error) { 
	homeDir, err := os.UserHomeDir()

	if err != nil { 
		fmt.error("No home directory found")
		return nil, err 
	}
	
	_,err := os.Stat("%s/blackbox", homeDir)
	if err != nil { 
		if os.IsNotExist(err) { 
			mkdirErr := os.MkdirAll("blackbox", 0777) // 0777 means all owners, groups and every1 eles have read wirte and exe perm. This will be revised after testing
			if mkdirErr != nil { 
				return nil, mkdirErr
			}


		}
		fmt.Println("Error: ", err)
	}

   


}
