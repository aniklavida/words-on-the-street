# Release checklist — v1.0

Nothing may be ticked because it should work. Each line is ticked when it has
been run.

Every line below was exercised on this branch by running the named test for
real, and the adversarial lines were exercised by breaking the code the claim
depends on, watching the named test fail, and restoring the code. Where a
sabotage was caught by a second, independent safeguard, that is said so rather
than reported as the named test's own assertion.

## Evidence

- [x] Every fetch produces a record with all required fields. — `TestFetch_AllRequiredFieldsPresent` (internal/fetch), `TestRecord_Validation` and `TestFetchQuery_EachSourceRecordsCompleteEvidence`.
- [x] **A fetch that skips the evidence record is not expressible** — demonstrated by attempting it. — `TestFetch_SkippingEvidenceRecordIsNotExpressible` (internal/fetch) invokes the Go compiler on two attempted violations. Reproduced by hand: a call with no store fails to build with `not enough arguments in call to fetch.Fetch`; assigning the result to `[]byte` fails with `cannot use 1st function result (value of type string) as []byte`. Both are compile errors, not runtime checks.
- [x] The hash covers the bytes as received; hashing normalised output instead fails a named test. — `TestFetch_HashCoversRawBytesNotNormalised` (internal/fetch). Sabotage: made `Fetch` hash the normalised bytes instead of the raw bytes; the named test failed. The defect was caught before the test's own assertion by three independent safeguards (`Record.Validate`, `FileStore/MemoryStore.Save`, and `store.Get`), each of which re-hashes the raw bytes and reports a payload/tampering mismatch. With those three disabled the test still failed at `store.Get`. Restored, passes.
- [x] Raw bytes are retrievable from the record after normalisation. — `TestFetch_RawBytesRetrievableAfterNormalisation` (internal/fetch), `TestStore_ContentAddressing` (internal/evidence).
- [x] A stored record cannot be silently altered. — `TestStore_AppendOnlyRefusesOverwrite` and `TestStore_ModifiedStoreIsDetectable` (internal/evidence).

## verify

- [x] Identical, changed and gone are distinguished, against a fixture mutated between runs. — `TestVerify_DistinguishesIdenticalChangedAndGone` (internal/verify), chained per source in `TestFetchQuery_VerifyChainsInitialFetchForEachSource`.
- [x] A changed page reports **what** changed, not only that it did. — `TestVerify_ChangedReportsWhatChanged` (internal/verify) asserts a line diff with the removed and added lines.
- [x] A deleted source is reported as gone, and the stored bytes are still retrievable. — `TestVerify_GoneStillRetrievableFromStore` (internal/verify).
- [x] Verification never overwrites the original record. — `TestVerify_NeverOverwritesOriginalRecord` (internal/verify) byte-compares the original record file and its record hash before and after a changed check.

## Failover

- [x] A forced primary failure completes on the fallback. — `TestMissingBackend_ProducesRecordedStateInEvidenceRecord` (internal/fetch, scenario 1) and `TestCLI_FetchFallbackWarnsOnStderrAndKeepsStdoutClean` (cmd/words-on-the-street).
- [x] The record names which backend served the result and whether it was primary. — `TestFetch_AllRequiredFieldsPresent` (backend name and `IsFallback`), `TestMissingBackend_ProducesRecordedStateInEvidenceRecord`.
- [x] Agent-facing output says when a fallback was used. — `TestCLI_FetchFallbackWarnsOnStderrAndKeepsStdoutClean` and `TestCLI_FetchHealthyPrimaryEmitsNoWarning` (cmd/words-on-the-street).
- [x] All backends failing produces an honest failure, never an empty success. — `TestFetchSource_AllBackendsFailingProducesHonestFailureWithRecord`, `TestFetchSource_AllBackendsFailErrorNamesEveryAttempt`, and `TestCLI_FetchAllBackendsFailNamesEveryAttempt`.

## Credentials

- [x] A credential-bearing fetch leaves no credential in the record **or** the agent payload — asserted separately, and searched across the whole store. — `TestFetch_CookieBearingFetchLeavesNoCredentialInStore` (whole-store walk), `TestTwitterCookie_NeverReachesRecordLogOrAgentOutput`, `TestLinkedIn_CookieNeverAppearsInRecordLogOrSynthesisOutput`, `TestMCPFetchTool_RedactsRegisteredSecretFromAgentOutput`.
- [x] Removing redaction fails a named test for **each** surface. — Sabotage run one guard at a time:
  - record arguments: `SanitizeArgs` neutralised → `TestSanitizeArgs_RedactsCredentials` and `TestSanitizeArgs_ScrubsRegisteredCookieValue` fail.
  - record validation: `Record.Validate` credential checks disabled → `TestRecord_RejectsCredentials` fails.
  - log line: `Redact` neutralised → `TestTwitterCookie_NeverReachesRecordLogOrAgentOutput` fails with `log line leaked the cookie`.
  - agent output: `Redact` neutralised → `TestMCPFetchTool_RedactsRegisteredSecretFromAgentOutput` fails with `agent output leaked the credential`.
  - synthesis: `Redact` neutralised → `TestClaimAndReasonAreRedacted` fails for both claim text and unreached reason.
  Each guard was restored and its test passes again.
- [x] The risk of raising a rate limit is disclosed to the user when they raise it, and the setting is not blocked. — `TestTwitterRateLimit_ConservativeByDefaultAndRaiseable` (internal/fetch) and `TestCLI_ConfigureTwitterShowsBanRiskDisclosure` (disclosure names `WORDS_ON_THE_STREET_TWITTER_RATE_LIMIT` and states the risk is not blocked).
- [x] The configuration flow shows the risk before accepting a credential. — `TestCLI_ConfigureTwitterShowsBanRiskDisclosure` and `TestCLI_ConfigureLinkedInShowsBanRiskDisclosureWithoutEchoingCookie`.
- [x] A session-cookie source sends the credential to the backend and records only the redacted placeholder. — `TestTwitterCookie_NeverReachesRecordLogOrAgentOutput`, `TestLinkedIn_CookieNeverAppearsInRecordLogOrSynthesisOutput` (both read the fixture's own received-args file first, so the assertion is not vacuous), `TestTwitter_CookieFromKeychainUsedForFetchWithoutEnvVar`, `TestLinkedIn_CookieFromKeychainUsedForFetchWithoutEnvVar`.
- [x] `configure twitter` prints the ban-risk disclosure before the cookie is set. — `TestCLI_ConfigureTwitterShowsBanRiskDisclosure` (cmd/words-on-the-street).

## Routing

- [x] A workplace-culture question demonstrably routes away from the source where nobody criticises anyone. — `TestRouting_WorkplaceCultureRoutesAwayFromUncriticalSource` (internal/synthesis).
- [x] A library-quality question routes to issue trackers and technical communities. — `TestRouting_LibraryQualityRoutesToIssueTrackersAndTechnicalCommunities` (internal/synthesis).
- [x] The chosen sources and the reason appear in the answer. — `TestRouting_ChosenSourcesAndReasonAppearInAnswer` (internal/synthesis), in both the rendered answer and the structured JSON.

## Synthesis

- [x] Every claim in an answer resolves to a stored record, checked mechanically. — `TestEveryClaimResolvesToStoredRecord` (internal/synthesis) resolves every attribution through `store.Get` and proves `Build` refuses a claim whose record is absent.
- [x] An unreachable source appears as unreached, not omitted. — `TestUnreachedSourceAppearsExplicitly` (internal/synthesis).
- [x] The representativeness caveat is present and cannot be configured away. — `TestCaveatCannotBeSuppressed` (internal/synthesis): the builder is not variadic and the config exposes no caveat switch.

## Security

- [x] A default run opens no connection outside the configured sources. — `TestEgress_DefaultSessionContactsOnlyConfiguredSourceEndpoints` (internal/security) runs a full session through the real orchestration with an egress guard and a recording backend, and checks every URL argument against the endpoints the source resolvers produce; `TestEgress_NoOutboundHTTPClientInCore` is the static half.
- [x] Injecting an outbound call into the core fails a named test. — Sabotage: added `http.Get("https://telemetry.example/beacon")` to `fetch.FetchQuery`. `TestEgress_NoOutboundHTTPClientInCore` failed on the static scan; `TestEgress_DefaultSessionContactsOnlyConfiguredSourceEndpoints` and `TestEgress_NoNetworkUntilSourceFetchExplicitlyTriggered` failed with the guard recording the request. The guard refuses before dialling, so no real network was reached. Restored, passes.
- [x] A hostile source configuration cannot execute a shell command. — `TestHostileSourceConfigurationCannotExecuteShell` and `TestHostileBackendCommandValueIsNeverShellInterpreted` (internal/security). Sabotage: changed `FetchSource` to join the command and arguments into one `sh -c` string; the named test failed with the canary file present. Restored, passes.
- [x] Release binaries publish checksums, and the documented install path uses them. — `TestReleaseBinariesPublishChecksums` and `TestDocumentedInstallPathVerifiesChecksums` (internal/security). Built for real from this machine: all five workflow targets (`linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`, `windows/amd64`) with `GOOS/GOARCH/CGO_ENABLED=0 go build -trimpath`; generated a sha256 `checksums.txt`; `shasum -a 256 --check checksums.txt` reported `OK` for all five; the darwin/arm64 binary runs (`version`). The built artifacts are not committed.
- [x] No install path instructs an agent to fetch and execute a remote document. — `TestHonesty_NoPublicDocumentInstructsFetchAndExecute` (scans every Markdown document), `TestDocumentedInstallPathVerifiesChecksums`, and the `No instruction may tell an agent to execute a remote document` step in `.github/workflows/validate.yml`.

## Honesty

- [x] The README carries all three limit sentences together. — `TestHonesty_ReadmeCarriesAllThreeLimitSentencesTogether` (internal/security), which reads README.md and docs/SPEC.md and requires the proved / not-proved / not-representative sentences and the statement that they ship together.
- [x] Release notes state plainly what is unverified, including endpoint instability on any source read through a user session. — `TestHonesty_ReleaseNotesStateUnverifiedEndpointInstability` (internal/security). The CHANGELOG now carries an explicit **Unverified** section naming the experimental `twitter`/`linkedin` session reads and their endpoint instability.
- [x] No public document names a reference project except where attribution is legally required. — `TestHonesty_NoPublicDocumentNamesAReferenceProject` (internal/security) scans every Markdown document against a list of comparable projects in this space. No attribution is legally required here, so the exception does not apply and none of the names appears.
