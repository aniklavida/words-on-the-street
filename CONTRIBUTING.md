# Contributing

Thank you for considering a contribution.

**Current state:** this repository is a specification. There is no code yet. The
most useful contribution today is telling us where the design is wrong.

## The rule that governs this project

**Evidence that misdescribes itself is worse than no evidence.**

A record saying a page was fetched at a time it was not, by a backend that did
not serve it, or with a command that was not the command that ran, is not a
weaker version of provenance — it is a false claim with a timestamp on it. Any
change that could make a record diverge from what actually happened is the most
serious kind of bug this project has.

## Claims

Every statement about what this project does is one of: **implemented and
tested**, **experimental**, **planned**, or **unsupported**. Nothing is
described as working until it has been run.

If you write a test, break the code it protects and confirm that named test
fails. **If it still passes, that is a finding, not a success** — work out which
of three things happened:

1. a second, independent safeguard is also enforcing it;
2. your edit did not apply, or did not compile — a build failure proves nothing;
3. the test never reaches the code you broke, and is therefore worthless.

Do not report something as verified because a sabotage run passed.

## Things a pull request must not do

- Add a code path that retrieves bytes without writing an evidence record.
- Put a credential anywhere it could be logged, recorded, synthesised or shown
  to an agent.
- Build a command as a string for execution. Argument arrays only.
- Add an install path that instructs an agent to fetch and execute a remote
  document.
- Implement scraping here. Backends are external processes, which is also what
  keeps their licences from constraining this one.

## Adding a source

Say which access it needs — open, key, or a user-supplied session — what it is
good for, and **what it distorts**. The second half matters as much as the
first: routing is not only where to look but how far to trust what is found.
