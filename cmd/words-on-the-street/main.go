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
	a := &app.App{Store: store, Registry: loadRegistry()}

	switch command {
	case "fetch":
		if len(os.Args) < 3 {
			fmt.Println("Usage: words-on-the-street fetch <source> <query>")
			fmt.Println("       words-on-the-street fetch <url> <backend> [args...]")
			os.Exit(1)
		}

		var hash string
		if _, isSource := a.Registry.BackendsForSource(os.Args[2]); isSource {
			// Source form: resolve the query, then fetch and record through the
			// source's registered backends.
			if len(os.Args) < 4 {
				fmt.Printf("Usage: words-on-the-street fetch %s <query>\n", os.Args[2])
				os.Exit(1)
			}
			hash, err = a.FetchQuery(context.Background(), os.Args[2], os.Args[3])
		} else {
			// Explicit form: a URL and a named backend, with optional extra args.
			if len(os.Args) < 4 {
				fmt.Println("Usage: words-on-the-street fetch <url> <backend> [args...]")
				os.Exit(1)
			}
			url := os.Args[2]
			backendName := os.Args[3]
			args := os.Args[4:]
			hash, err = a.Fetch(context.Background(), backendName, args, url, "1.0", false)
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

		// The record hash goes to stderr so stdout stays exactly the fetched
		// bytes; anything piping the payload is unaffected, and a caller that
		// wants to re-check the fetch has the identifier to do it.
		fmt.Fprintf(os.Stderr, "record: %s\n", hash)
		fmt.Print(string(rec.Payload))

	case "verify":
		if len(os.Args) < 3 {
			fmt.Println("Usage: words-on-the-street verify <record-hash>")
			os.Exit(1)
		}
		result, err := a.Verify(context.Background(), os.Args[2])
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("state: %s\n", result.State)
		fmt.Printf("original_hash: %s\n", result.OriginalHash)
		fmt.Printf("original_record_hash: %s\n", result.OriginalRecordHash)
		if result.CurrentHash != "" {
			fmt.Printf("current_hash: %s\n", result.CurrentHash)
		}
		fmt.Printf("observation_record_hash: %s\n", result.ObservationRecordHash)
		if result.Diff != "" {
			fmt.Printf("diff:\n%s", result.Diff)
		}

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

// loadRegistry returns the shipped default sources, or the registry described by
// the WORDS_ON_THE_STREET_REGISTRY document when that is set. The registry is
// data, so a machine can point at the backends it actually has without a code
// change.
func loadRegistry() *backend.Registry {
	path := os.Getenv("WORDS_ON_THE_STREET_REGISTRY")
	if path == "" {
		return backend.DefaultRegistry()
	}
	reg, err := backend.LoadRegistryFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to load registry from %q: %v\n", path, err)
		os.Exit(1)
	}
	return reg
}
