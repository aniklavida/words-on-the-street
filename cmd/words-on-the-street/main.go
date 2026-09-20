package main

import (
	"context"
	"fmt"
	"os"
	"runtime/debug"

	"github.com/aniklavida/words-on-the-street/internal/app"
	"github.com/aniklavida/words-on-the-street/internal/backend"
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
	var store evidence.Store
	fileStore, err := evidence.DefaultStore()
	if err != nil {
		store = evidence.NewMemoryStore()
	} else {
		store = fileStore
	}
	reg := backend.DefaultRegistry()
	a := &app.App{Store: store, Registry: reg}

	switch command {
	case "fetch":
		if len(os.Args) < 4 {
			fmt.Println("Usage: words-on-the-street fetch <url> <backend-or-source> [args...]")
			os.Exit(1)
		}
		url := os.Args[2]
		backendOrSource := os.Args[3]
		args := os.Args[4:]

		var hash string
		if _, isSource := a.Registry.BackendsForSource(backendOrSource); isSource {
			hash, err = a.FetchSource(context.Background(), backendOrSource, url, args)
		} else {
			hash, err = a.Fetch(context.Background(), backendOrSource, args, url, "1.0", false)
		}

		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}

		rec, err := a.Store.Get(hash)
		if err != nil {
			fmt.Printf("Error retrieving evidence: %v\n", err)
			os.Exit(1)
		}

		fmt.Print(string(rec.Payload))

	case "verify":
		fmt.Println("verify: not implemented")
		os.Exit(1)

	case "doctor":
		reports := backend.CheckAllHealth(context.Background(), a.Registry)
		hasError := false
		for src, reps := range reports {
			fmt.Printf("Source %s:\n", src)
			for _, rep := range reps {
				fmt.Printf("  - %s: %s (version: %s, range: %s, licence: %s)\n",
					rep.BackendName, rep.Status, rep.DetectedVersion, rep.DeclaredRange, rep.Licence)
				if rep.Status != backend.StatusReachable {
					hasError = true
				}
			}
		}
		if hasError {
			os.Exit(1)
		}

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

		fmt.Printf("words-on-the-street %s\n", info.Main.Version)
		fmt.Printf("Build: %s\n", revision)
		fmt.Printf("Signed: unsupported\n")

	default:
		fmt.Printf("Unknown command: %s\n", command)
		os.Exit(1)
	}
}
