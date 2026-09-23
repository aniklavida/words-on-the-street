# LinkedIn source — session cookie, risk, and rate limits

LinkedIn is read through a session cookie **you** supply. There is no automated
login here and no attempt to bypass an access control: you bring a session your
own browser already established, the same way a browser export or an extension
would. The project never sees or stores your password.

## Configuring the cookie

The preferred storage is your OS keychain, with a fallback to the environment
variable:

1. **OS keychain** (`words-on-the-street` / `linkedin`): store the cookie directly:
   ```
   words-on-the-street config linkedin --store
   ```
2. **Environment variable** (fallback for headless environments or CI):
   ```
   export WORDS_ON_THE_STREET_LINKEDIN_COOKIE='li_at=...; JSESSIONID=...'
   ```

Resolution order:
1. OS keychain, if available and a cookie is stored.
2. `WORDS_ON_THE_STREET_LINKEDIN_COOKIE` environment variable.

Before you configure it, run:

```
words-on-the-street config linkedin
```

That command prints the ban-risk disclosure and resolution order in plain text at
the CLI. It never echoes the cookie value, whether or not one is already set.

Resolution refuses a LinkedIn query, before any backend runs, when no cookie is
configured in either the keychain or the environment. That is deliberate: there
is no unauthenticated fallback and no silent, degraded fetch.

## The risk, stated plainly

- **Automated access is against LinkedIn's terms of service**, even when you
  supply your own session.
- **A session cookie grants access to your account without the password and past
  two-factor authentication.** Anyone who obtains it can act as you.
- **LinkedIn bans permanently and without warning and has litigated against
  scrapers.** This is not a generic "your account may be limited" warning.
  Losing the account the cookie belongs to is a real and likely outcome.

Use a separate, dedicated account that you can afford to lose. Never configure a
cookie for an account you depend on.

This is a disclosure, not an enforcement step. Nothing in the tool asks you to
acknowledge the risk before proceeding; the decision is yours.

## Rate limits

Requests are limited to a conservative default of **15 seconds** between calls.
You can raise or lower the minimum interval with:

```
export WORDS_ON_THE_STREET_LINKEDIN_MIN_INTERVAL='45s'
```

The value is any Go duration (`30s`, `2m`, `0`). An invalid value falls back to
the conservative default. The risk of raising the limit is disclosed once, here,
rather than gated behind a runtime confirmation.

## What is never kept

The cookie travels only as a single request-header argument to the external
backend. It is redacted by the same path every other credential goes through
before any record is written, and it is never printed, stored in the evidence
record, written to a log, used in a synthesis, or passed to an agent. A
registered value is also refused by the record if it appears anywhere in it.
