package main

import (
	"fmt"
	"os"
)

func runInit() {
	fmt.Println("init ran")
}

func runServe() {
	fmt.Println("serve subcommand called")
}

func runLog() {
	fmt.Println("log request successful")
}

func runShow(sessionID string) {
	fmt.Println("showing session ", sessionID)
}

func runRevert(turnID string) {
	fmt.Println("reverting turn ", turnID)
}

func runBlame(fileAndLine string) {
	fmt.Println("issue blamed on" + fileAndLine)
}

func runStatus() {
	fmt.Println("status of BlackBox requested")
}

func main() {
	if len(os.Args) < 2 {
		fmt.Print("to run BlackBox: BlackBox <command_name> \n")
		os.Exit(1)
	}

	// check cmd and return corrasponding action(s)
	switch os.Args[1] {
	case "init":
		runInit()
	case "serve":
		runServe()
	case "log":
		runLog()
	case "show":
		if len(os.Args) < 3 {
			fmt.Print("session id required. \n", "command usage: BlackBox show <session_id>")
			os.Exit(1)

		}
		runShow(os.Args[2])
	case "revert":
		if len(os.Args) < 3 {
			fmt.Print("turn id required. \n",
				"command usage: BlackBox revert <turn_id>")
			os.Exit(1)

		}
		runRevert(os.Args[2])

	case "blame":
		if len(os.Args) < 3 {
			fmt.Print("file name and line is required \n",
				"command usage: BlackBox blame <file:line>")
			os.Exit(1)
		}
		runBlame(os.Args[2])
	case "status":
		runStatus()
	default:
		fmt.Print("unknown command: ", os.Args[1])
	}
}
