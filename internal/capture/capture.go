package capture

import(
	"encoding/json"
	"github.com/mkdirRON/BlackBox/internal/db"
	"os"
	"io"
	"crypto/rand"
	"encoding/hex"
)


type UserPromptSubmit struct {
	SessionID     string `json:"session_id"`
	HookEventName string `json:"hook_event_name"`
	Cwd           string `json:"cwd"`
	Prompt        string `json:"prompt"`
}

type PostToolUse struct {
	SessionID     string `json:"session_id"`
	HookEventName string `json:"hook_event_name"`
	Cwd           string `json:"cwd"`
	ToolName      string `json:"tool_name"`
	ToolInput      struct { 
		Filepath 		string `json:"file_path"`
		Content 		string `json:"content"`
	} `json:"tool_input"` 
}


type Handler struct{ 
	DB *db.DB
}

func (d *Handler) HandleHook() error { 
	jsonData, err := io.ReadAll(os.Stdin)	
	if err != nil{ 
		return err 
	}
 	
	usrPromt := UserPromptSubmit{}
	err = json.Unmarshal(jsonData, &usrPromt)
	if err != nil{ 
		return err
	}

	err = d.DB.InsertSession(usrPromt.SessionID, usrPromt.Cwd)
	if err != nil{ 
		return err
	}
	
	b := make([byte[], 8])
	rand.Read(b)
	turnID := hex.EncodeToString(b)

	err = d.DB.InsertTurn(turnID, usrPromt.SessionID, usrPromt.Prompt, 0 )
	if err != nil {
		return err
	}
	
	return nil
 	
}



