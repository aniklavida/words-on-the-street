// Package security holds the tests that prove the security claims this project
// makes, rather than asserting them.
//
// The claims are the ones in SECURITY.md and AGENTS.md: the core opens no
// network connection except to a configured source; there is no telemetry under
// any configuration; a hostile source configuration cannot reach a shell; and
// distribution is a checksummed binary rather than a fetch-and-execute
// instruction.
//
// This package deliberately contains no production code. Every file here is a
// test whose name states the claim it defends; when the code enforcing a claim
// is broken, the named test fails. That is the point of the package: a claim
// that cannot fail this way is a claim that is not being checked.
package security
