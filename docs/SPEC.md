# Words on the Street — specification

**Status:** planned. Nothing in this document is implemented.
**Updated:** 2026-09-18

## What it is

Ask a question about a person, a company, a product or a library. This works out
where to look, gathers what people said, answers with every claim attributed,
and keeps the evidence so the answer can be checked after the sources have
changed.

The fetching is done by proven external tools. What this adds is the record.

## The five stages

```
your question
      ↓
ROUTE        which sources suit this question, and how far to trust each
      ↓
FETCH        gather, through external tools
      ↓
EVIDENCE     record URL, time, hash, backend, path taken
      ↓
SYNTHESIS    answer, every claim attributed
      ↓
WATCH        ask again later — what changed
```

Existing tools do stage two. This is the other four.

## 1 · Route

Every source has a predictable bias. Routing is not only *where to look* but
**how far to trust what is found there**.

| Source kind | Good for | What it distorts |
|---|---|---|
| Professional networks | career history, company pages | nobody writes anything negative |
| Microblogs | immediate reaction | extremes and hype; quiet people invisible |
| Forums | candid opinion; anonymity buys honesty | skews bitter; the aggrieved post more |
| Issue trackers | real, reproducible problems | only people who had a problem write |
| Technical communities | depth | narrow, self-selecting |
| News | verified, attributable | late, often press-release driven |

*"How is it working at company X?"* routes to candid sources and away from the
one where nobody criticises anyone. *"Is library Y any good?"* routes to issue
trackers and technical communities. Getting this wrong produces a confident
answer assembled from the wrong crowd.

Routing ships as Markdown rather than compiled in: it is knowledge, and it is
the part most likely to be corrected by people who know a community better than
we do.

## 2 · Fetch

Backends are external processes, invoked with argument arrays. This project
implements no scraping.

| Access | Requirement |
|---|---|
| Open API | nothing |
| Free API | an account and a key |
| User-supplied session | the user's own logged-in session |

The third row carries real costs and they are stated where a user meets them,
not in a footnote: automated access is against those platforms' terms; at least
one bans permanently and without warning; and a session cookie grants account
access without the password and past two-factor authentication.

Consequences, as product requirements:

- The risk is shown **at the point of configuring a session**.
- A separate account is recommended for the platform that bans permanently.
- Rate limits are conservative by default; the user can adjust them, with the
  risk of raising a limit disclosed once rather than gated on an acknowledgement.
- A credential never enters the record, the synthesis, the log, or anything the
  agent sees.

There is no automated login, and no bypassing of an access control beyond using
a session the user knowingly supplied.

## 3 · Evidence

Every fetch records:

```
resolved URL
UTC timestamp
content hash of the bytes as received, before any normalisation
backend name and version
whether the path was primary or a fallback
```

Raw bytes stored content-addressed beside the record. Append-only.

**`verify`** re-fetches a recorded entry and reports identical, changed, or
gone. That command is most of the value: it is what makes an answer checkable
after the internet has moved on.

### Why this corpus specifically

Social media rewrites itself. Posts are edited, accounts deleted, people deny
having said things. If a decision rests on what someone said and the evidence
evaporates, what remains is an agent's recollection.

## 4 · Synthesis

Not *"people complain about X"* but *"four forum posts, three issue reports and
one microblog post; the most recent is 2 September"* — each resolving to a record
that can be re-verified.

- A claim with no record behind it does not appear in an answer.
- Coverage is stated, including what could not be reached.
- No sentiment score is presented as objective.

## 5 · Watch

Ask the same question again: what is new, what was deleted, what was edited and
what it said before, which way the tone moved.

This falls out of the evidence store almost for free, which is exactly why it
needs a boundary. **For v1.0: a manual re-run reporting differences. No
scheduling, no notifications, no background process.** The moment it grows a
daemon it is a different product with different operational questions, and v1.0
is not the place to discover that.

## The claim, and its three limits

**What is proved:** these exact bytes were received from this URL at this time by
this backend, and whether they have changed since.

**What is not proved:** that the bytes were true, that the source was honest, or
that the platform showed the same thing to anyone else. A faithful record of a
lie is still a faithful record of a lie.

**What can be scraped is not representative.** People who post are not people.
Issue trackers are written by those who hit a problem. Professional networks
contain no criticism. The honest answer to *"what do people say about X"* is
always *"the people who wrote something say this"*.

All three ship together.

## Version 1.0 scope

**In:** routing with per-source trust weighting; four to six sources chosen for
demonstrable difference rather than coverage count; the evidence record;
`verify`; `watch` as a manual re-run; credential handling with the safeguards
above; `doctor`; CLI and MCP.

**Explicitly out:** competing on source count; any scraping implemented here; a
hosted anything; bypassing access controls; sentiment scoring presented as
objective; scheduling or background processes.

## Differentiation

Breadth is not contested. An established tool covers more sources and does it
well.

The difference is the record and the routing. An agent that can say *"this is
what I read, here is the hash, here is when, and yes it has changed since"* —
and *"I did not ask that source because nobody criticises anyone there"* — is
doing something breadth does not provide.

**If provenance turns out not to be what people want, this is a worse version of
a good tool.** That is the risk, and it is the first thing to test.
