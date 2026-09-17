# Words on the Street — contributor guidelines

These apply to any agent working in this repository, and to any human reviewing
what an agent produced.

## What this project is

A single binary that answers a question by gathering what people said, and keeps
a record of exactly what it read. Routing knowledge ships as Markdown beside it;
everything that must not be forgotten lives in the binary.

## Why it is a binary and not a skill

The promise is the evidence. An agent that fetches a page and forgets to hash it
leaves nothing behind — there is no degraded version, there is no product. An
instruction an agent must remember to follow will be skipped under pressure,
which is true of people and ticket boards too.

`fetch` **is** the recording. They are not two actions, so there is no order in
which one happens without the other. Any change that separates them is a design
change, not an implementation detail.

## The rule that governs everything

**Evidence that misdescribes itself is worse than no evidence.**

A record naming a command that did not run, a backend that did not serve the
bytes, or a time that is not when it happened, is a false claim with a timestamp
on it. Guard this above features.

## Claims

Use exactly one of: **implemented and tested**, **experimental**, **planned**,
**unsupported**.

If you write a test, break the code it protects and confirm that named test
fails. **If it still passes, that is a finding, not a success** — work out which
of three:

1. a second, independent safeguard is also enforcing it;
2. your edit did not apply, or did not compile — a build failure proves nothing;
3. the test never reaches the code you broke, and is therefore worthless.

A sabotage run that passes is never reported as verification.

## Hard rules

- No code path retrieves bytes without writing an evidence record.
- A credential never reaches a record, a log, a synthesis, or an agent.
- Backends are invoked with argument arrays. Never a command string.
- No install path instructs an agent to fetch and execute a remote document.
- No automated login. No bypassing an access control.
- No scraping implemented here.

## Before committing

- No absolute machine paths, no credentials, no unresolved merge markers.
- `gofmt -l .` empty, `go vet ./...` clean, `go test ./...` green.
- Commit messages describe what changed and why, in prose.
