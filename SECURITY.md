# Security policy

## Reporting

Report a security issue through GitHub's private vulnerability reporting on this
repository. Please do not open a public issue for something that could be
exploited before it is fixed.

## What this project handles that is worth attacking

**Session credentials.** Some sources cannot be read without a session the user
supplies. A session cookie grants account access without the password and past
two-factor authentication. Anything that causes one to be logged, recorded,
synthesised, or shown to a connected agent is a serious vulnerability, not a
cosmetic one.

**External processes.** Fetching is done by invoking other tools. A source
configuration that reaches a shell, or that causes an unexpected binary to run,
is a vulnerability.

**The evidence store.** A record that can be silently rewritten is not evidence.
A way to alter a stored record or its hash without detection is a vulnerability.

## Two things this project will not do, by design

**No install path instructs an agent to fetch and execute a remote document.**
That pattern is remote command execution with the blast radius of the user's
machine, and it holds only until someone changes the document. Distribution is
checksummed binaries.

**No automated login, and no bypassing of access controls** beyond using a
session the user knowingly supplied. A report that this project could read more
by circumventing a platform's controls is a feature request we will decline.

## What is not a vulnerability

- A source changing its endpoints and breaking a backend. That is expected and
  the documentation says so.
- A platform rate-limiting or banning an account used with a supplied session.
  The risk is stated at the point of configuration.
