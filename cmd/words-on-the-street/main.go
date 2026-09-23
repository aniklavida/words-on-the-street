package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"runtime/debug"
	"strings"

	"github.com/aniklavida/words-on-the-street/internal/app"
	"github.com/aniklavida/words-on-the-street/internal/backend"
	"github.com/aniklavida/words-on-the-street/internal/dashboard"
	"github.com/aniklavida/words-on-the-street/internal/evidence"
	"github.com/aniklavida/words-on-the-street/internal/fetch"
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

	// A configured session cookie is registered with the redaction set before
	// any command can print or record anything, so the value is scrubbable from
	// the first boundary onward. The value itself is not kept here.
	_, _ = fetch.TwitterCookie()

	switch command {
	case "fetch":
		if len(os.Args) < 3 {
			fmt.Println("Usage: words-on-the-street fetch <source> <query>")
			fmt.Println("       words-on-the-street fetch <url> <backend> [args...]")
			os.Exit(1)
		}

		var hash string
		sourceName := ""
		if _, isSource := a.Registry.BackendsForSource(os.Args[2]); isSource {
			// Source form: resolve the query, then fetch and record through the
			// source's registered backends.
			if len(os.Args) < 4 {
				fmt.Printf("Usage: words-on-the-street fetch %s <query>\n", os.Args[2])
				os.Exit(1)
			}
			sourceName = os.Args[2]
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
			fmt.Printf("Error: %s\n", evidence.Redact(err.Error()))
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

		// Degradation is surfaced where the fetch happens, not only in the
		// record. It goes to stderr so stdout remains exactly the fetched
		// bytes, and it is derived from the record so the warning and the
		// record cannot describe different backends. A degraded fetch still
		// exits 0: succeeding on a fallback is the whole point of the failover.
		if sourceName != "" && rec.IsFallback {
			fmt.Fprintf(os.Stderr, "%s\n", evidence.Redact(degradationWarning(sourceName, rec)))
		}

		fmt.Print(string(rec.Payload))

	case "verify":
		if len(os.Args) < 3 {
			fmt.Println("Usage: words-on-the-street verify <record-hash>")
			os.Exit(1)
		}
		result, err := a.Verify(context.Background(), os.Args[2])
		if err != nil {
			fmt.Printf("Error: %s\n", evidence.Redact(err.Error()))
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

	case "status":
		statuses := backend.CheckAllStatus(context.Background(), a.Registry)
		unhealthy := false
		for _, st := range statuses {
			switch st.State {
			case backend.SourceHealthy:
				fmt.Printf("source: %s, status: healthy (primary %s, serving %s)\n",
					st.Source, st.Primary, st.Serving)
			case backend.SourceDegraded:
				fmt.Printf("source: %s, status: degraded (primary %s unavailable, serving fallback %s)\n",
					st.Source, st.Primary, st.Serving)
				unhealthy = true
			default:
				fmt.Printf("source: %s, status: down (no backend reachable, primary %s)\n",
					st.Source, st.Primary)
				unhealthy = true
			}
		}
		if unhealthy {
			os.Exit(1)
		}

	case "configure":
		// The disclosure is the point of configuring a session, not a footnote.
		// It is plain text at the CLI the user runs before setting the variable,
		// so the risk is read where the decision is made. It explains the
		// setting; it does not accept the cookie as an argument, so the value
		// cannot land in shell history or in a process argument list.
		if len(os.Args) < 3 || os.Args[2] != "twitter" {
			fmt.Println("Usage: words-on-the-street configure twitter")
			fmt.Println("  Prints the ban-risk disclosure for the twitter session cookie.")
			os.Exit(1)
		}
		fmt.Print(twitterDisclosure)

	case "mcp":
		mcpServer := mcpserver.NewServer(a)
		if err := server.ServeStdio(mcpServer); err != nil {
			fmt.Printf("MCP error: %v\n", err)
			os.Exit(1)
		}

	case "serve":
		// The dashboard is a separate command rather than a flag on `mcp` because
		// `mcp` owns stdout for the JSON-RPC stdio transport; an HTTP server that
		// logs or renders on the same stream would corrupt it. A caller that wants
		// both runs the two commands side by side.
		lister, ok := store.(evidence.Lister)
		if !ok {
			fmt.Println("Error: the configured evidence store cannot be listed")
			os.Exit(1)
		}
		addr := dashboard.DefaultAddr
		if len(os.Args) >= 3 {
			addr = os.Args[2]
		}
		ln, err := dashboard.Listen(addr)
		if err != nil {
			fmt.Printf("Error: %s\n", evidence.Redact(err.Error()))
			os.Exit(1)
		}
		dash := &dashboard.Server{Records: lister, Registry: a.Registry}
		fmt.Fprintf(os.Stderr, "Dashboard listening on http://%s (loopback only, read-only)\n", ln.Addr())
		if err := http.Serve(ln, dash.Handler()); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Printf("Dashboard error: %v\n", err)
			os.Exit(1)
		}

	case "config":
		// Configuration is an environment variable, matching the store and
		// registry. This command is the point of configuration: it shows the
		// ban-risk disclosure in plain text before the user exports the cookie,
		// and it never echoes the value.
		if len(os.Args) < 3 {
			fmt.Println("Usage: words-on-the-street config linkedin")
			os.Exit(1)
		}
		switch os.Args[2] {
		case "linkedin":
			fmt.Println(fetch.LinkedInBanRiskDisclosure)
			if _, err := fetch.LinkedInCookie(); err != nil {
				fmt.Printf("\nNo cookie is configured yet. Export %s, then run `words-on-the-street fetch linkedin <query>`.\n", fetch.LinkedInCookieEnv)
			} else {
				fmt.Printf("\nA cookie is configured in %s. Its value is never printed, recorded, or shown to an agent.\n", fetch.LinkedInCookieEnv)
			}
		default:
			fmt.Printf("Unknown config target: %s\n", os.Args[2])
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

// twitterDisclosure is printed by `configure twitter`, the point where a user
// learns how to supply the session cookie. It is plain text at the CLI rather
// than a line in a document, because the risk has to arrive where the decision
// is made. It states the ban risk and the fact that a cookie is account access
// without a password; it states the rate-limit risk once and does not gate the
// setting behind a confirmation, matching the project's "disclosure, not
// enforcement" decision.
const twitterDisclosure = `Configuring the twitter source

Twitter/X automated access is against Twitter/X's terms. A session cookie is
not a password, but it grants account access without the password and past
two-factor authentication. Twitter/X bans accounts permanently and without
warning for this, so use a separate account you are willing to lose.

The session cookie is read from the environment:
  WORDS_ON_THE_STREET_TWITTER_COOKIE

It is never written to an evidence record, a log line, a synthesis output, or
anything an agent sees.

Requests are limited to 5 per minute by default, which is deliberately
conservative. Raise the limit with:
  WORDS_ON_THE_STREET_TWITTER_RATE_LIMIT=<requests-per-minute>

Raising the limit increases the chance of a ban. That risk is stated once, here
and in the docs; the setting is not blocked and needs no acknowledgement.

No automated login happens. You supply a session you already have, exactly as a
browser export would.
`

// degradationWarning describes a successful fetch that did not use the primary
// backend. It names the backend that served the bytes and every earlier backend
// that failed, with the status recorded for it, so a human or a calling script
// can tell degraded operation from a clean primary fetch. The text is built
// from the evidence record, so the warning cannot name a different backend than
// the record does.
func degradationWarning(source string, rec *evidence.Record) string {
	skipped := make([]string, 0, len(rec.BackendAttempts))
	for _, attempt := range rec.BackendAttempts {
		if attempt.Status == "" {
			skipped = append(skipped, attempt.BackendName)
			continue
		}
		skipped = append(skipped, fmt.Sprintf("%s (%s)", attempt.BackendName, attempt.Status))
	}
	if len(skipped) == 0 {
		skipped = append(skipped, rec.MissingBackends...)
	}
	return fmt.Sprintf("⚠ %s: primary backend unavailable, used fallback (%s); skipped: %s",
		source, rec.BackendName, strings.Join(skipped, ", "))
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
