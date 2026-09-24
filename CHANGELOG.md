# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project uses
[semantic versioning](https://semver.org/spec/v2.0.0.html).

## Unreleased

### Added — implemented, not yet released

Everything below exists in the repository. None of it has been tagged or
published as a release, so it is listed here rather than under a version.

- **Core binary** (`words-on-the-street`) with CLI and MCP surfaces.
- **Evidence recording:** content-addressed append-only evidence store (`~/.words-on-the-street/evidence`) and ledger.
- **Backend registry:** external process invocation with argument arrays, version pinning, licensing metadata, and health checks.
- **Query resolvers:** resolution for `hacker-news`, `lobsters`, `web`, `github`, `twitter`, and `linkedin`.
- **Verification:** `verify` command to re-fetch and check if content is identical, changed, or deleted, recording observation records.
- **Watch:** `watch` command for synchronous diff reports across queries over time.
- **Routing:** knowledge base and source trust weighting skill with CLI and MCP override tracking.
- **Credentials:** OS keychain storage with environment fallback for Twitter/X and LinkedIn session cookies, with ban-risk disclosures and secret redaction.
- **Local dashboard:** embedded loopback-only read-only HTTP dashboard (`serve`).
- **Synthesis:** attributed claims library enforcing provenance and representativeness caveat.
- **Security posture:** egress enumeration tests, hostile configuration validation, and checksummed release workflow.
- **Documentation:** user guide covering installation, configuration, cookie risks, source costs, and known limitations.

[Unreleased]: https://github.com/aniklavida/words-on-the-street/commits/main
