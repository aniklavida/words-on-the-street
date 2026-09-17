package main

import (
	"context"
	"fmt"
	"os"
	"runtime/debug"

	"github.com/aniklavida/words-on-the-street/internal/app"
	"github.com/aniklavida/words-on-the-street/internal/evidence"
	"github.com/aniklavida/words-on-the-street/internal/mcpserver"
	"github.com/mark3labs/mcp-go/server"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: words-on-the-street <command>")
		os.Exit(1)
	}

	command := os.Args[1]
	store := &evidence.MemoryStore{} // Using MemoryStore for foundation
	a := &app.App{Store: store}

	switch command {
	case "fetch":
		if len(os.Args) < 4 {
			fmt.Println("Usage: words-on-the-street fetch <url> <backend>")
			os.Exit(1)
		}
		url := os.Args[2]
		backend := os.Args[3]
		rec, err := a.Fetch(context.Background(), backend, nil, url, "1.0", false)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(string(rec.Payload))

	case "verify":
		fmt.Println("verify: not implemented")
		os.Exit(1)

	case "doctor":
		fmt.Println("doctor: not implemented")
		os.Exit(1)

	case "mcp":
		mcpServer := mcpserver.NewServer(a)
		if err := server.ServeStdio(mcpServer); err != nil {
			fmt.Printf("MCP error: %v\n", err)
			os.Exit(1)
		}

	case "version":
		info, ok := debug.ReadBuildInfo()
		if !ok {
			fmt.Println("version: unknown (no build info)")
			return
		}
		
		// Check vcs.revision to see if it was built from a git commit
		revision := "unknown"
		modified := false
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" {
				revision = setting.Value
			}
			if setting.Key == "vcs.modified" && setting.Value == "true" {
				modified = true
			}
		}

		if modified {
			revision += " (modified)"
		}

		// A signature check isn't standard in go build info, but let's report what we have
		fmt.Printf("words-on-the-street %s\n", info.Main.Version)
		fmt.Printf("Build: %s\n", revision)
		// For signed status, standard Go binaries aren't signed out of the box in a way ReadBuildInfo sees, 
		// but we can report that it is not signed as a placeholder or check GOEXPERIMENT etc.
		// Wait, "whether it was signed". I'll just state "Signed: unknown" as a placeholder for foundation.
		fmt.Printf("Signed: unknown\n")

	default:
		fmt.Printf("Unknown command: %s\n", command)
		os.Exit(1)
	}
}
