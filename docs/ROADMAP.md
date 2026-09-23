# Roadmap

Nothing below is implemented.

**Open sources before anything that needs a credential.** Everything hard about
this product — evidence, failover, verification — is easier to get right when a
mistake cannot get anyone banned.

## 1 · Foundation

One binary, one core, CLI and MCP surfaces.

*Done when:* CLI and MCP produce identical results for the same request, and **a
fetch that skips the evidence record is not expressible** — demonstrated by
attempting it.

## 2 · The evidence record

Schema, append-only store, content addressing.

*Done when:* every field is present on every fetch; hashing normalised output
instead of raw bytes fails a named test; a credential-bearing fetch leaves
nothing in the store, searched whole rather than per-record.

## 3 · Backend registry

Ordered backends per source, version pinning, health checks, licence recorded
per backend.

*Done when:* an unexpected backend version is reported rather than accepted, and
a missing backend is a recorded state rather than a silent skip.

## 4 · The first two open sources

Two sources needing no credential, working completely.

*Done when:* real questions return results with complete records, and a rate
limit is handled honestly rather than retried blindly.

## 5 · `verify`

Re-fetch and compare: identical, changed, or gone.

*Done when:* all three are distinguished against a fixture mutated between runs;
a changed page reports **what** changed; a deleted source is reported as gone
and the stored bytes are still retrievable.

## 6 · Failover made visible

*Done when:* a forced primary failure completes on the fallback, the record
names which backend served the result, and all backends failing produces an
honest failure rather than an empty success.

## 7 · Routing

Source selection and trust weighting, shipped as Markdown.

*Done when:* a workplace-culture question demonstrably routes away from the
source where nobody criticises anyone, and the chosen sources and the reason
appear in the answer.

## 8 · Credentials

Storage, redaction at every boundary, conservative rate limits, the risk stated
at the point of configuration.

*Done when:* removing redaction fails a named test **for each surface
separately**, the risk of raising a rate limit is disclosed clearly once when
the user raises it, and the setting is not blocked.

## 9 · The sources that need a session

Only after 8.

Twitter/X is the first. Its resolver, its cookie configuration and the redaction
guard are **implemented and tested**; live retrieval through a supplied session
is **experimental**.

*Done when:* the source resolves and records through the same `Sources()` /
`FetchQuery` path as the open sources; removing redaction fails a named test for
each surface separately; and the ban risk, including that a cookie grants account
access without the password, is shown at the point of configuration.

## 10 · Synthesis

Answers where every claim resolves to a stored record.

**Implemented and tested** as the library `internal/synthesis`; wiring it to a
CLI command or MCP tool is **planned**.

*Done when:* an unreachable source appears in the answer as unreached rather
than omitted, and the representativeness caveat cannot be configured away.

*Met:* `TestEveryClaimResolvesToStoredRecord` resolves every claim hash through
`store.Get` and refuses a claim whose record is absent;
`TestUnreachedSourceAppearsExplicitly` names a failed and an outcome-less
source; `TestCaveatCannotBeSuppressed` shows the caveat survives the emptiest
answer and both renderers and that the builder takes no variadic option and the
config exposes no caveat switch.

## 11 · `watch`

A manual re-run reporting what changed. **No scheduling, no notifications, no
background process** — the moment this grows a daemon it becomes a different
product with different operational questions.

## 12 · Security posture

Egress enumeration, install proof, hostile-configuration proof.

## 13 · Release

Cross-platform binaries with published checksums, and documentation carrying the
claim and its three limits together.
