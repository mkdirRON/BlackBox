# BlackBox: An audit trail creator for AI coding sessions

## What is it? 
BlackBox is a local developer tool that creates an audit trail for AI coding sessions. It hooks into Claude Code's lifecycle events and records every prompt you send alongside the exact file diffs that prompt produced.

## Core Functionality 


```Bash
blackbox init — installs hooks into .claude/settings.json
blackbox serve — opens a local web UI at localhost:7331 showing a session timeline
blackbox log — lists recent sessions in the terminal
blackbox show <session-id> — shows all turns in a session
blackbox revert <turn-id> — reverses the diffs from a specific turn using git apply --reverse
blackbox blame <file:line> — tells you which prompt last touched that line
blackbox status — shows DB path and session count
```
```

#STILL IN DEVELOPMENT. core features had no meaningful function other than for developer testing. Its still a skeleton 
