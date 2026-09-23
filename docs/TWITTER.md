# Twitter/X source — session cookie, risk, and rate limits

Twitter/X is read through a session cookie **you** supply. There is no automated
login here and no attempt to bypass an access control: you bring a session your
own browser already established, the same way a browser export or an extension
would. The project never sees or stores your password.

## Configuring the cookie

The preferred storage is your OS keychain, with a fallback to the environment
variable:

1. **OS keychain** (`words-on-the-street` / `twitter`): store the cookie directly:
   ```
   words-on-the-street configure twitter --store
   ```
2. **Environment variable** (fallback for headless environments or CI):
   ```
   export WORDS_ON_THE_STREET_TWITTER_COOKIE='auth_token=...'
   ```

Resolution order:
1. OS keychain, if available and a cookie is stored.
2. `WORDS_ON_THE_STREET_TWITTER_COOKIE` environment variable.

Before you configure it, run:

```
words-on-the-street configure twitter
```

That command prints the ban-risk disclosure and resolution order in plain text at
the CLI. It never echoes the cookie value, whether or not one is already set.

## The risk, stated plainly

- **Automated access is against Twitter/X's terms of service**, even when you
  supply your own session.
- **A session cookie grants access to your account without the password and past
  two-factor authentication.** Anyone who obtains it can act as you.
- **Twitter/X bans accounts permanently and without warning.** Use a separate,
  dedicated account that you can afford to lose. Never configure a cookie for an
  account you depend on.

This is a disclosure, not an enforcement step. Nothing in the tool asks you to
acknowledge the risk before proceeding; the decision is yours.

## Rate limits

Requests are limited to a conservative default of **5 per minute**. You can raise
the limit with:

```
export WORDS_ON_THE_STREET_TWITTER_RATE_LIMIT=10
```

The risk of raising the limit is disclosed once, here, rather than gated behind a
runtime confirmation.

## What is never kept

The cookie travels only as a request-header argument to the external backend. It
is redacted by the same path every other credential goes through before any
record is written, and it is never printed, stored in the evidence record,
written to a log, used in a synthesis, or passed to an agent. A registered value
is also refused by the record if it appears anywhere in it.
