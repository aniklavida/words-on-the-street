# Architecture

**Status:** planned. Nothing here is implemented.

## Why a binary and not a skill

**The test: does the product's core promise depend on something an agent could
forget?**

A skill that teaches an agent how to work still helps when a step is skipped;
the knowledge was delivered. **This product's promise is the evidence.** If the
agent fetches a page and forgets to hash it, there is no degraded version —
there is nothing. A month later the question *"where did you read that?"* has no
answer, which is the exact failure this project exists to prevent.

An instruction an agent must remember to follow will be skipped under pressure.
That is not a criticism of agents; it is true of people and ticket boards too.

With a binary, `fetch` **is** the recording. They are not two actions, so there
is no order in which one happens without the other.

There is a second reason. This project argues that installation must be a
checksummed package rather than an instruction to execute a remote document. A
skill that told an agent to run shell commands to fetch and hash pages would be
doing a milder version of the thing it criticises.

**A skill still ships alongside** — the routing knowledge, which is genuinely
knowledge and belongs in Markdown. But the evidence guarantee lives in the
binary, not in an agent's judgement.

## Form

- **Language:** Go. One checksummed binary makes the installation argument
  materially easier to keep than a package that runs install scripts.
- **Core:** routing, fetch orchestration, evidence recording, verification,
  synthesis assembly.
- **Surfaces:** CLI and MCP over the same core, never two implementations.

## Backends are external processes

This project implements no scraping. Fetching is done by invoking proven tools
as separate processes, with argument arrays rather than command strings.

Invoking rather than linking is also what keeps their licences — including
copyleft ones — from constraining this one. **That makes "invoked as a separate
process" a claim which has to stay true**, not a convenience. No backend is ever
linked or embedded, and the licence of each is recorded before it becomes a
shipped default.

## The evidence store

```
record        resolved URL · UTC timestamp · content hash · backend name and
              version · primary or fallback
payload       raw bytes, content-addressed, stored beside the record
```

The hash covers the bytes **as received, before any normalisation**. A record
that hashes normalised output cannot answer whether the source changed, which is
the one question it exists to answer. Normalisation is recorded separately so it
never destroys what arrived.

Append-only. A record that can be silently rewritten is not evidence.

## Failover, and why it is not silent

Each source has an ordered list of backends with declared version ranges. When
one fails, the next is tried.

The common design makes this invisible and sells the invisibility: *backends come
and go, you won't notice.* For reliability that is right, and the mechanism is
adopted here. But invisible failover means the agent cannot tell that an answer
came from a degraded path returning less.

**Reliability without silence:** fail over, and record which path was taken.

## Credentials

Held in the OS keychain where available, with a documented fallback. Redacted at
every boundary — the record, the synthesis, the log, agent-facing output.

Asserted separately for each surface. One passing assertion says nothing about
the others.

## Directory shape

```
cmd/words-on-the-street/   CLI entry point
internal/route/            source selection and trust weighting
internal/fetch/            backend orchestration
internal/evidence/         the record and its store
internal/verify/           re-fetch and compare
internal/synthesis/        answers with attribution
internal/mcpserver/        MCP transport
routing/                   the routing skill, as Markdown
schemas/                   record and configuration schemas
docs/                      this specification and its siblings
```
