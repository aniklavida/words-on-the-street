# Release checklist — v1.0

Nothing may be ticked because it should work. Each line is ticked when it has
been run.

## Evidence

- [ ] Every fetch produces a record with all required fields.
- [ ] **A fetch that skips the evidence record is not expressible** — demonstrated by attempting it.
- [ ] The hash covers the bytes as received; hashing normalised output instead fails a named test.
- [ ] Raw bytes are retrievable from the record after normalisation.
- [ ] A stored record cannot be silently altered.

## verify

- [ ] Identical, changed and gone are distinguished, against a fixture mutated between runs.
- [ ] A changed page reports **what** changed, not only that it did.
- [ ] A deleted source is reported as gone, and the stored bytes are still retrievable.
- [ ] Verification never overwrites the original record.

## Failover

- [ ] A forced primary failure completes on the fallback.
- [ ] The record names which backend served the result and whether it was primary.
- [ ] Agent-facing output says when a fallback was used.
- [ ] All backends failing produces an honest failure, never an empty success.

## Credentials

- [ ] A credential-bearing fetch leaves no credential in the record **or** the agent payload — asserted separately, and searched across the whole store.
- [ ] Removing redaction fails a named test for **each** surface.
- [ ] The risk of raising a rate limit is disclosed to the user when they raise it, and the setting is not blocked.
- [ ] The configuration flow shows the risk before accepting a credential.
- [ ] A session-cookie source sends the credential to the backend and records only the redacted placeholder.
- [ ] `configure twitter` prints the ban-risk disclosure before the cookie is set.

## Routing

- [ ] A workplace-culture question demonstrably routes away from the source where nobody criticises anyone.
- [ ] A library-quality question routes to issue trackers and technical communities.
- [ ] The chosen sources and the reason appear in the answer.

## Synthesis

- [ ] Every claim in an answer resolves to a stored record, checked mechanically.
- [ ] An unreachable source appears as unreached, not omitted.
- [ ] The representativeness caveat is present and cannot be configured away.

## Security

- [ ] A default run opens no connection outside the configured sources.
- [ ] Injecting an outbound call into the core fails a named test.
- [ ] A hostile source configuration cannot execute a shell command.
- [ ] Release binaries publish checksums, and the documented install path uses them.
- [ ] No install path instructs an agent to fetch and execute a remote document.

## Honesty

- [ ] The README carries all three limit sentences together.
- [ ] Release notes state plainly what is unverified, including endpoint instability on any source read through a user session.
- [ ] No public document names a reference project except where attribution is legally required.
