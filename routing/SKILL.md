---
name: routing
description: Route research questions to appropriate sources and determine source trustworthiness based on structural platform biases in Words on the Street.
---

# Routing and source trust weighting skill

Decide where to look, and how far to trust what is found there, for a given question.

Routing is **knowledge**, not mechanism. It ships as this Markdown skill beside the
`words-on-the-street` binary so connecting agents read it and choose sources
accordingly, rather than relying on a rigid algorithm compiled into the binary.

The binary guarantees the evidence; this skill governs source selection and
trust weighting.

## The six real sources

Words on the Street implements and registers backends for six real sources:

1. `hacker-news`: Technical depth; narrow, self-selecting audience.
2. `lobsters`: Curated technical discussions; smaller and even narrower demographic.
3. `github`: Real, reproducible bugs and issue trackers; negative selection bias (only people who hit a problem write in).
4. `web`: General breadth over depth; uncurated, subject to SEO manipulation and marketing content.
5. `twitter`: Immediate reaction and breaking alerts; skews toward extremes, outrage, and hype.
6. `linkedin`: Verifiable employment history; nobody writes anything negative about current or recent employers.

*Note:* Hypothetical sources like Reddit or Stack Overflow are not implemented in
the binary and must not be routed to. Route strictly against these six real
sources.

---

## Source trust weighting matrix

| Source | Good for | What it distorts | Trust weighting |
|---|---|---|---|
| `hacker-news` | Technical depth, systems architecture, postmortems, engineer opinions on tooling. | Narrow, self-selecting audience (tech workers, startup founders); contrarian consensus; loud minorities. | High for technical architecture and postmortems; medium-low for mass-market or non-technical topics. |
| `lobsters` | Curated technical discussions, language and systems internals, security reviews. | Even narrower demographic than HN; invite-only insularity; smaller sample sizes. | High for deep technical and language debates; low for industry-wide adoption trends. |
| `github` (issues) | Real, reproducible bugs, active regressions, maintainer responsiveness, edge cases. | Only people who hit a problem write in; silent satisfied majority is invisible; issue threads can turn hostile during breaking changes. | High for operational failure modes and regression checks; low for broad user sentiment. |
| `web` | General search breadth over depth, documentation, technical blog posts, release announcements. | Zero inherent curation; SEO spam, affiliate marketing, outdated guides, sponsored reviews. | High for primary technical docs and release specs; low for unbiased comparison. |
| `twitter` | Immediate reaction, real-time breaking alerts, security advisories, release announcements. | Outrage and hype cycles; tribal polarization; quiet, measured analysis is invisible; character limits discard context. | High for immediate incident awareness; low for deep technical evaluations. |
| `linkedin` | Verifiable employment history, corporate organizational structure, official company announcements. | Performative optimism and self-censorship; nobody criticizes a current or recent employer on their public profile; PR cheerleading. | High for career timelines and corporate announcements; zero for candid workplace culture or internal morale. |

---

## Worked example 1: Workplace culture

### The question

> *"How is it working at Company X? What is the engineering culture and management like?"*

### Routing instructions

1. **Identify the goal:** Obtain candid, unvarnished accounts of engineering
   practices, on-call expectations, compensation, and management culture.
2. **Evaluate `linkedin`:**
   LinkedIn profiles are tied to real names, professional reputations, and future
   job opportunities. Employees face severe professional and contractual penalties
   for speaking negatively about their employer.
   **Action: EXPLICITLY ROUTE AWAY from `linkedin`.**
   Relying on LinkedIn will produce uniform, sanitized praise and corporate PR.
3. **Evaluate `hacker-news` and `lobsters`:**
   Both platforms allow pseudonymous accounts and host candid discussions about
   work environments, layoff retrospectives, and management behavior.
   **Action: ROUTE TOWARD `hacker-news` and `lobsters`.**
4. **Evaluate `github`:**
   If Company X maintains open-source repositories, review public issue triage and
   PR interactions to see how engineers communicate under pressure.
   **Action: ROUTE TOWARD `github` if public repos exist.**

### Route record

- **Chosen sources:** `hacker-news`, `lobsters`, `github` (if open-source repos exist)
- **Excluded source:** `linkedin`
- **Reason:** "Workplace culture questions require candid accounts of internal conditions. LinkedIn is systematically distorted by self-preservation and cheerleading, where negative feedback is virtually never posted. Hacker News and Lobsters provide pseudonymous accounts of engineering conditions, while GitHub issues expose real collaborative interaction."

---

## Worked example 2: Library and tool quality

### The question

> *"Is library Y any good? Should we adopt it in production?"*

### Routing instructions

1. **Identify the goal:** Identify real production reliability, breaking changes,
   concurrency bugs, and maintainer responsiveness.
2. **Evaluate `twitter`:**
   Twitter rewards snappy promotional threads, virality, and hype ("X is 10x
   faster than Y!"). It lacks reproduction steps, benchmarks, and architectural
   rigor.
   **Action: EXPLICITLY ROUTE AWAY from `twitter`.**
   Evaluating software on Twitter confuses marketing buzz with stability.
3. **Evaluate `github`:**
   Issue trackers contain concrete, reproducible bugs, active memory leaks, and
   regression reports.
   *Caveat:* Remember negative selection bias — users only open issues when
   something breaks.
   **Action: ROUTE TOWARD `github`.**
4. **Evaluate `hacker-news` and `lobsters`:**
   Technical forums host thorough architectural breakdowns, comparison
   discussions, and production postmortems by teams running libraries at scale.
   **Action: ROUTE TOWARD `hacker-news` and `lobsters`.**

### Route record

- **Chosen sources:** `github`, `hacker-news`, `lobsters`
- **Excluded source:** `twitter`
- **Reason:** "Evaluating library quality requires concrete bug reports and in-depth architectural critiques. Twitter skews toward promotional hype and shallow takes. GitHub issues expose real production defects, while Hacker News and Lobsters offer comparative analysis from experienced practitioners."

---

## Recording user overrides

When a user explicitly requests a source contrary to the routing model (e.g.
requesting a Twitter search for a library evaluation to check social chatter, or
LinkedIn for workplace culture):

1. Honor the user's explicit intent.
2. Record the override on the fetch call:
   - CLI: pass `--override` and `--override-reason "<reason>"`
   - MCP: pass `"routing_override": true` and `"routing_override_reason": "<reason>"`
3. The override is preserved in the append-only evidence record and is
   permanently distinguishable from a recommended fetch.

---

## Output contract for synthesized answers

When generating a multi-source synthesized answer (or when implementing the
planned synthesis stage in milestone 10):

1. **Chosen sources and rationale:** Must state every source fetched and why it
   was selected.
2. **Excluded sources and rationale:** Must state relevant sources deliberately
   avoided and why.
3. **Override indicator:** Must report whether any source was an override against
   routing recommendations.
4. **Attributed claims:** Every factual claim must reference its evidence record
   hash (`record: <hash>`).
5. **Limits statement:** Must include the three product boundaries:
   - Bytes, time, and hash are proved.
   - Truth of the statements is not proved.
   - Poster unrepresentativeness caveat cannot be omitted.
