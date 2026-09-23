# Words on the Street

**What people are saying, and proof they said it.**

Ask a question about a person, a company, a product or a library. Words on the Street works out where to look, gathers what people actually said, answers with every claim attributed — and **keeps the evidence**, so the answer can still be checked a month later when the posts have been edited or deleted.

> **Status: planned.** Nothing here is implemented yet. This repository currently contains the specification, architecture and roadmap only. Every claim below describes the intended product, not working software.

## The problem

Agents can already reach the web. The gap is not access; it is **accountability for what came back**.

- **No provenance.** An agent reads a thread and makes a recommendation. Which post? At what time? Has it been edited since, or deleted? Nothing records it, and the conclusion outlives its evidence.
- **Silent backend substitution.** Access layers route each source through a primary tool with fallbacks and switch when one breaks. Good for reliability — but the agent cannot then say which tool produced an answer, or that it quietly dropped to a degraded path returning less.
- **Install by remote instruction.** The common pattern hands an agent a URL to an install document and lets it execute what the document says. That is remote command execution with the blast radius of your machine, and it holds until someone changes the document.
- **No sense of which source lies about what.** Every platform has a bias, and treating them as interchangeable produces confident nonsense.

## The five stages

```
your question
      ↓
ROUTE        which sources suit this question, and how far to trust each
      ↓
FETCH        gather, through proven external tools
      ↓
EVIDENCE     record URL, time, hash, backend, path taken
      ↓
SYNTHESIS    answer, every claim attributed
      ↓
WATCH        ask again later — what changed
```

Existing tools do stage two. This is the other four.

## Routing — where to look, and how far to trust it

Every source has a predictable bias:

| Source | Good for | What it distorts |
|---|---|---|
| Professional networks | career history, company pages | **nobody writes anything negative** |
| Microblogs | immediate reaction | extremes and hype; quiet people are invisible |
| Forums | candid opinion, anonymity buys honesty | skews bitter; the aggrieved post more |
| Issue trackers | real, reproducible problems | **only people who had a problem write** |
| Technical communities | depth | narrow, self-selecting |
| News | verified, attributable | late, often press-release driven |

So *"how is it working at company X?"* routes to candid sources and away from the one where nobody criticises anyone. Getting this wrong produces a confident answer assembled from the wrong crowd.

## Evidence

Every fetch records the resolved URL, a UTC timestamp, a content hash of the bytes **as received before any normalisation**, the backend name and version, and whether the path was primary or a fallback. Raw bytes are stored content-addressed beside the record.

`verify` re-fetches a recorded entry and reports **identical, changed, or gone.**

That one command is most of the point. Social media is the corpus that rewrites itself: posts are edited, accounts deleted, people deny having said things. If a decision rests on what someone said and the evidence evaporates, what remains is an agent's recollection.

## The claim, and its three limits

**What is proved:** these exact bytes were received from this URL at this time by this backend, and whether they have changed since.

**What is not proved:** that the bytes were true, that the source was honest, or that the platform showed the same thing to anyone else. **A faithful record of a lie is still a faithful record of a lie.**

**What can be scraped is not representative.** People who post are not people. Issue trackers are written by those who hit a problem. Professional networks contain no criticism. The honest answer to *"what do people say about X"* is always **"the people who wrote something say this"** — and this product says so rather than selling a biased sample as opinion.

All three ship together. None is published without the other two.

## Design commitments

- **Installation is a package, never an instruction.** Checksummed release binaries. No install path tells an agent to fetch and execute a remote document.
- **Fetching and recording are one operation.** There is no code path that retrieves bytes without writing an evidence record.
- **Backends are external processes**, invoked with argument arrays, never shell strings. This project implements no scraping of its own.
- **Degradation is visible.** A failover is recorded and surfaced, not hidden.
- **No telemetry, ever**, under any configuration.

## Sources need different things

| Access | Requirement |
|---|---|
| Open API | nothing |
| Free API | an account and a key |
| Session cookie | **your own logged-in session — see below** |

Some platforms cannot be read at useful depth without a session you supply yourself. Where that is so, the documentation states the risk at the point of configuring it rather than in a footnote: automated access is against those platforms' terms, one of them bans permanently and without warning, and a session cookie grants account access without the password and past two-factor authentication.

A credential never enters the evidence record, the synthesis, the log, or anything the agent sees. The LinkedIn source is the first to need a session cookie; its configuration and rate-limit risks are documented in [docs/LINKEDIN.md](docs/LINKEDIN.md).

## Documentation

- [Specification](docs/SPEC.md)
- [Architecture](docs/ARCHITECTURE.md)
- [Roadmap](docs/ROADMAP.md)
- [Release checklist](docs/RELEASE_CHECKLIST.md)
- [LinkedIn source: cookie, risk and rate limits](docs/LINKEDIN.md)
- [Routing and source trust weighting](docs/ROUTING.md)

## Licence

MIT. See [LICENSE](LICENSE).
