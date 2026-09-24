# Words on the Street

**What people are saying, and proof they said it.**

Ask a question about a person, a company, a product or a library. Words on the Street works out where to look, gathers what people actually said, answers with every claim attributed — and **keeps the evidence**, so the answer can still be checked a month later when the posts have been edited or deleted.

> **Status: planned** (for v1.0 release). The core binary (evidence recording, backend registry, query resolvers, verify, watch, doctor, status, local dashboard, MCP server) is **implemented and tested**. Live retrieval through session cookies (Twitter/X and LinkedIn) is **experimental**. An automated end-to-end synthesis command is **planned**. Hosted services, scraping implemented in-repo, and bypassing access controls are **unsupported**.

## The claim, and its three limits

Every time Words on the Street describes what it proves, these three sentences ship together:

1. **What is proved:** these exact bytes were received from this URL at this time by this backend, and whether they have changed since.
2. **What is NOT proved:** that the bytes were true, that the source was honest, or that the platform showed the same thing to anyone else. A faithful record of a lie is still a faithful record of a lie.
3. **What can be scraped is not representative.** People who post are not people. The honest answer to "what do people say about X" is always "the people who wrote something say this."

All three ship together. What is proved, What is not proved, and the not representative limit travel together; none of these three sentences may be published without the other two, anywhere in the docs.

## The problem

Agents can already reach the web. The gap is not access; it is **accountability for what came back**.

- **No provenance.** An agent reads a thread and makes a recommendation. Which post? At what time? Has it been edited since, or deleted? Nothing records it, and the conclusion outlives its evidence.
- **Silent backend substitution.** Access layers route each source through a primary tool with fallbacks and switch when one breaks. Good for reliability — but the agent cannot then say which tool produced an answer, or that it quietly dropped to a degraded path returning less.
- **Install by remote instruction.** The common pattern hands an agent a URL to an install document and lets it execute what the document says. That is remote command execution with the blast radius of your machine, and it holds until someone changes the document.
- **No sense of which source lies about what.** Every platform has a bias, and treating them as interchangeable produces confident nonsense.

## The five stages

```
your question
      ↓
ROUTE        which sources suit this question, and how far to trust each
      ↓
FETCH        gather, through proven external tools
      ↓
EVIDENCE     record URL, time, hash, backend, path taken
      ↓
SYNTHESIS    answer, every claim attributed
      ↓
WATCH        ask again later — what changed
```

Existing tools do stage two. This is the other four.

## Why installation is a package (not a copy-paste instruction)

This project does not provide a copy-paste shell command or tell an agent to fetch and execute a remote script. That is a deliberate architectural requirement, not a generic preference:

1. **The security boundary.** The common pattern hands an agent or user a URL to a remote script and pipes downloaded content directly into a shell interpreter. That is unverified remote code execution with the blast radius of the host machine. A compromised server or mutated document changes what executes without warning.
2. **The atomic evidence guarantee.** In this codebase, fetching and evidence recording are not two distinct steps that an agent or script can choose to separate; they are compiled together into a single atomic operation inside the Go binary (`fetch.FetchQuery`). If installation were a set of shell instructions, an agent or human operator could run a fetch and skip the content hashing or ledger recording under pressure. With a compiled binary, `fetch` **is** the recording — no byte can be retrieved from any backend without immediately writing an immutable, content-addressed evidence record to disk.
3. **Cryptographic verification.** Standalone release binaries are published with an accompanying `checksums.txt` file containing the SHA-256 digest of every platform binary. Verifying this digest before running ensures you execute the exact, audited code published for that release.

## Installation

Releases publish standalone binaries for Linux, macOS, and Windows with a `checksums.txt` manifest. Full verification instructions are in [docs/INSTALL.md](docs/INSTALL.md).

### 1 · Download release binary and checksums

Download the release binary for your operating system and architecture alongside `checksums.txt` from the Releases page into the same directory:

- Linux: `words-on-the-street_linux_amd64` or `words-on-the-street_linux_arm64`
- macOS: `words-on-the-street_darwin_amd64` or `words-on-the-street_darwin_arm64`
- Windows: `words-on-the-street_windows_amd64.exe`

### 2 · Verify the checksum

Verify that the downloaded binary matches the published SHA-256 digest before running it:

On macOS:
```bash
shasum -a 256 --check checksums.txt
```

On Linux:
```bash
sha256sum --check --ignore-missing checksums.txt
```

On Windows (PowerShell):
```powershell
Get-FileHash .\words-on-the-street_windows_amd64.exe -Algorithm SHA256
```
Compare the output digest against the matching line in `checksums.txt`. If it differs, stop: do not execute the binary.

Make the binary executable (on Unix):
```bash
chmod +x words-on-the-street_*
mv words-on-the-street_* words-on-the-street
```

### Building from source

Alternatively, build the binary directly with Go (1.27+):

```bash
go build -trimpath -o words-on-the-street ./cmd/words-on-the-street
```

Verify your build:
```bash
./words-on-the-street version
```

### 3 · Check environment prerequisites

Words on the Street invokes external tools as separate processes to retrieve data; it implements no scraping of its own. Check that required backends are installed and healthy on your machine:

```bash
./words-on-the-street doctor
```

And verify the operational status of all sources:

```bash
./words-on-the-street status
```

## Sources: requirements and costs

Words on the Street ships with six supported sources. Each source has specific backend dependencies, credential requirements, and operational costs:

| Source | Backend Tool | Credentials / Access Needed | Cost | Trust Weighting & Purpose |
|---|---|---|---|---|
| `hacker-news` | `curl` (>= 7.68.0) | None (open Algolia Search API) | $0 | High for technical architecture, postmortems, and engineering trade-offs. Narrow technical demographic. |
| `lobsters` | `curl` (>= 7.68.0) | None (open `.json` feeds and site search) | $0 | High for nuanced language/systems critiques. Small, invite-only community sample. |
| `web` | `curl` (>= 7.68.0) | None (direct public URL fetching) | $0 | Broad coverage for specifications and release notes. Skewed by SEO, marketing, and corporate copy. |
| `github` | `gh` (>= 2.0.0), fallback `curl` | Optional GitHub account (`gh auth login`) for higher rate limits (5,000 req/hr vs 60 unauthenticated) | $0 | High for reproducible bugs, regressions, and triage activity. Negative selection bias: only users with issues write. |
| `twitter` | `curl` (>= 7.68.0) | User-supplied session cookie (`auth_token`) | $0 (fees) / High account ban risk | High for breaking incidents and real-time awareness. Skewed by outrage, hype, and polarization. |
| `linkedin` | `curl` (>= 7.68.0) | User-supplied session cookie (`li_at`, `JSESSIONID`) | $0 (fees) / Severe account ban risk | High for employment tenure and corporate announcements. Zero for workplace culture (performative cheerleading). |

## Configuring cookie sources and the cookie risk

Two sources (`twitter` and `linkedin`) do not provide open search APIs for this use and require a session cookie from an account you already hold in your browser.

### The cookie risk, stated plainly

Before configuring a cookie-based source, you must understand the following risks:

- **Automated access violates terms of service.** Both Twitter/X and LinkedIn prohibit automated access in their terms of service, even when you supply your own session.
- **A session cookie provides full account access.** A session cookie is not an API token with scoped permissions. It grants full access to your account without requiring your password and bypasses two-factor authentication (2FA). Anyone who obtains this cookie can act as your account.
- **Accounts face permanent bans without warning.** Both platforms employ aggressive behavioral heuristics to detect automated requests. Twitter/X bans accounts permanently without warning. LinkedIn bans accounts permanently and has actively litigated against scraping activities. Losing the account associated with the cookie is a real and likely outcome.
- **Use dedicated burner accounts only.** Never configure a session cookie from a personal, professional, or organizational account you depend on. Only use separate, disposable accounts that you can afford to lose.
- **Credential safety in this tool:** The session cookie travels only as a single request-header argument (`-H "Cookie: ..."`) to the external `curl` process. It is scrubbed by the core redaction engine before any record is written. A session cookie is **never** printed to stdout/stderr, never written to an evidence record or ledger, never included in logs or synthesis output, and never exposed to an AI agent.

### How to configure session cookies

You can inspect the configuration status and view the ban-risk disclosure directly in your terminal at any time:

```bash
./words-on-the-street configure twitter
./words-on-the-street config linkedin
```

#### Option A: OS Keychain (recommended)

Storing cookies in your operating system's native keychain (macOS Keychain, Linux Secret Service / Keyring, Windows Credential Manager) prevents cookies from lingering in shell histories or environment files:

```bash
# Prompts securely for the cookie without echoing:
./words-on-the-street configure twitter --store
./words-on-the-street config linkedin --store
```

#### Option B: Environment variables (headless / CI environments)

For headless servers or environments where an OS keychain is unavailable:

```bash
# Twitter/X session cookie:
export WORDS_ON_THE_STREET_TWITTER_COOKIE='auth_token=your_auth_token_here'

# LinkedIn session cookie:
export WORDS_ON_THE_STREET_LINKEDIN_COOKIE='li_at=your_li_at_cookie; JSESSIONID=your_jsession_id'
```

Resolution order checks the OS keychain first, then falls back to the environment variable. If no cookie is configured, LinkedIn queries are refused immediately before executing any backend; Twitter queries attempt an unauthenticated fetch.

#### Rate limits

To protect accounts against rapid automated detection, conservative rate limits are applied:

- **Twitter/X:** Limited to 5 requests per minute by default. You can adjust this with:
  ```bash
  export WORDS_ON_THE_STREET_TWITTER_RATE_LIMIT=10
  ```
- **LinkedIn:** Limited to a minimum interval of 15 seconds between requests by default. You can adjust this with:
  ```bash
  export WORDS_ON_THE_STREET_LINKEDIN_MIN_INTERVAL=30s
  ```

Raising these limits increases the probability of an account ban. That risk is disclosed here and in the CLI; adjustments are not blocked by confirmation prompts.

## Quick start: asking one real question

A stranger can run the tool immediately from a clean environment without configuring credentials by querying an open source.

### 1 · Ask a question (Fetch)

Ask what people are discussing about a technology on Hacker News:

```bash
./words-on-the-street fetch hacker-news "sqlite"
```

What happens:
- **`stdout`** receives the clean, raw payload bytes exactly as served by the backend, unedited and un-normalized.
- **`stderr`** outputs the SHA-256 hash of the evidence record:
  ```
  record: 88b06620176e3440fd16ac4ca3051387c784df060824e9b137784ad197685564
  ```
- The immutable record and raw bytes are permanently committed to the local append-only evidence store (`~/.words-on-the-street/evidence`).

To inspect the raw payload in pretty JSON format:
```bash
./words-on-the-street fetch hacker-news "sqlite" 2>/dev/null | jq .
```

### 2 · Re-check provenance (Verify)

Social media posts are deleted, edited, and rewritten. The `verify` command re-fetches the recorded URL using the recorded backend and version, comparing the current response against the stored original:

```bash
./words-on-the-street verify 88b06620176e3440fd16ac4ca3051387c784df060824e9b137784ad197685564
```

Output:
```
state: identical
original_hash: 88b06620176e3440fd16ac4ca3051387c784df060824e9b137784ad197685564
original_record_hash: 2ee54d33b8b0069570c483ccf5323457ae89cca0e8aac7fd75d0888c8613ecbf
current_hash: 88b06620176e3440fd16ac4ca3051387c784df060824e9b137784ad197685564
observation_record_hash: 3f2e642b21caa01611c6b5436fbb7df480dbd091aac9824c8c352bb36d1e5c2d
```

`state` reports one of three outcomes:
- `identical`: The page served the exact same bytes.
- `changed`: The content has changed since the initial fetch; a unified diff is displayed.
- `gone`: The endpoint returned a 404, 410, or could not be reached.

Every verification check writes its own immutable observation record into the store, tracking the history of changes over time.

### 3 · Compare queries over time (Watch)

The `watch` command re-runs a query against a source, comparing results against prior fetches in the evidence store:

```bash
./words-on-the-street watch hacker-news "sqlite"
```

Output:
```
source: hacker-news
query: sqlite
resolved_url: https://hn.algolia.com/api/v1/search?query=sqlite
new: 0
deleted: 0
edited: 0
```

`watch` is a synchronous command. It does not run background processes, daemons, or cron jobs.

### 4 · Explore evidence in the local dashboard

Words on the Street includes an embedded, read-only local dashboard served by the binary:

```bash
./words-on-the-street serve
```

Open `http://127.0.0.1:8080` in your browser. The dashboard displays recent fetches, payload inspections, verification histories, and backend health status. It binds strictly to loopback (`127.0.0.1`), contains no write endpoints, and makes no network connections.

### 5 · Connect an AI coding agent (MCP)

To connect an AI agent (such as Claude Desktop or any MCP-compatible client), run the Model Context Protocol (MCP) server over standard I/O:

```bash
./words-on-the-street mcp
```

The MCP server exposes `fetch`, `verify`, `doctor`, and `status` tools with identical validation, redaction, and recording guarantees as the CLI.

## Routing and trust weighting

Every platform has a predictable bias. Routing is not only *where to look* but **how far to trust what is found there**.

Routing knowledge ships as Markdown in [docs/ROUTING.md](docs/ROUTING.md) and [routing/SKILL.md](routing/SKILL.md) rather than compiled into the binary. This keeps the trust model transparent, inspectable, and editable without recompilation.

If you intentionally query a source that contradicts routing recommendations, record your rationale with an override:

```bash
./words-on-the-street fetch --override --override-reason "Checking social buzz despite hype bias" twitter "sqlite"
```
The override and reason are permanently recorded in the evidence ledger.

## Known limitations

- **Endpoint instability on cookie-based platforms:**
  - **Dynamic JavaScript shells:** Twitter/X and LinkedIn frequently update their internal React/GraphQL frameworks and anti-scraping defenses. Because Words on the Street delegates fetching to `curl` rather than running a full headless browser (such as Chromium), requests to these platforms may return empty hydration shells, client-side scripts devoid of server-rendered text, or redirect to login/CAPTCHA walls. Live fetching for `twitter` and `linkedin` is explicitly **experimental**.
  - **Session invalidation:** Platform sessions expire naturally, are revoked upon password changes, or are terminated when platform security flags unfamiliar IP addresses or request frequencies.
- **The three limits:**
  1. **What is proved:** these exact bytes were received from this URL at this time by this backend, and whether they have changed since.
  2. **What is NOT proved:** that the bytes were true, that the source was honest, or that the platform showed the same thing to anyone else. A faithful record of a lie is still a faithful record of a lie.
  3. **What can be scraped is not representative.** People who post are not people. The honest answer to "what do people say about X" is always "the people who wrote something say this."
- **Platform selection bias:**
  - GitHub issue trackers document defects; satisfied users do not open issues.
  - LinkedIn is shaped by career self-censorship; negative opinions are absent.
  - Technical forums (Hacker News, Lobsters) represent narrow, self-selecting engineering demographics.
- **No scraping implemented in-repo:** Words on the Street relies on external CLI tools (`curl`, `gh`) invoked as separate processes. It will not bypass paywalls, solve CAPTCHAs, or perform automated logins.
- **Local-only operation:** The local dashboard (`serve`) binds strictly to loopback addresses (`127.0.0.1`). There is no multi-user mode, no remote authentication, and no hosted cloud service.
- **No background automation:** There are no background daemons, scheduled cron jobs, or notification systems. `watch` is a manual, foreground command.

## Documentation

- [Specification](docs/SPEC.md) — The five pipeline stages and product requirements.
- [Architecture](docs/ARCHITECTURE.md) — Why this is a Go binary and not a skill.
- [Installation](docs/INSTALL.md) — Checksum verification and build instructions.
- [Routing and trust weighting](docs/ROUTING.md) — Platform bias matrix and synthesis contract.
- [Twitter/X source details](docs/TWITTER.md) — Session cookie storage, risks, and rate limits.
- [LinkedIn source details](docs/LINKEDIN.md) — Session cookie storage, risks, and rate limits.
- [Roadmap](docs/ROADMAP.md) — Planned milestones and implementation progress.
- [Release checklist](docs/RELEASE_CHECKLIST.md) — Pre-release verification procedure.

## Licence

MIT. See [LICENSE](LICENSE).
