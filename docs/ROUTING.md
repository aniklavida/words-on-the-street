# Routing and source trust weighting

**Status:** implemented and tested (routing knowledge, bias model, CLI and MCP override recording). Synthesis contract is planned.

## Why routing ships as Markdown and not in the binary

The core guarantee of Words on the Street lives in the binary: no byte can be
retrieved without recording an immutable, content-addressed evidence record.
That is mechanism, and mechanism must not depend on an agent remembering to
follow instructions.

Routing, by contrast, is **knowledge**. Deciding where to look for a given
question and how far to trust what is found there requires understanding the
social dynamics, community norms, and systemic biases of each platform. A
compiled routing algorithm would be rigid, opaque, and difficult to calibrate as
platforms evolve. Shipping routing knowledge as Markdown alongside the binary
allows connecting agents and human users to inspect the trust model, apply it
consistently, and refine it without recompiling the binary.

The connecting coding agent reads this document, evaluates the user's question,
and selects the appropriate sources before calling `fetch`.

## The six real sources

This routing specification is written strictly against the six real sources
supported by this project's backend registry and resolvers:

1. `hacker-news`
2. `lobsters`
3. `github`
4. `web`
5. `twitter`
6. `linkedin`

Earlier design notes referenced hypothetical sources like Reddit or Stack
Overflow. Those platforms have no registered backends or query resolvers in this
codebase. In keeping with the repository's rule that public claims describe only
working software, the routing model here covers only the six real, shipped
sources.

## Source trust weighting matrix

Every platform has a predictable bias. Routing is not only *where to look* but
**how far to trust what is found there**.

| Source | Good for | What it distorts | Trust weighting |
|---|---|---|---|
| `hacker-news` | Technical depth, architectural postmortems, scalability debates, engineer sentiment on developer tooling. | Narrow, self-selecting audience (primarily US tech, startup, and systems engineer demographic); vocal contrarians; strong opinions often presented as industry consensus. | High for technical architecture, postmortems, and engineering trade-offs. Medium-low for non-technical company policies or mass-market consumer sentiment. |
| `lobsters` | Curated technical discussions, language internals, systems design, security evaluations. | Even narrower demographic than Hacker News; invite-only curation creates insular viewpoints and technical orthodoxy; low volume means small sample sizes. | High for nuanced language and systems critiques. Low for broad industry adoption statistics or non-technical topics. |
| `github` (issues) | Real, reproducible bugs, active edge cases, regression reports, maintainer responsiveness, upgrade friction. | Extreme negative selection bias: only users who encountered a failure or bug write in. The silent majority of satisfied users is completely invisible. Issue threads can skew hostile during breaking changes. | High for verifying specific operational failures and bug frequencies. Low for overall library satisfaction or developer happiness. |
| `web` | General breadth over depth, official documentation, project blogs, benchmark reports, long-form tutorials across unindexed domains. | No inherent curation or peer review; heavily distorted by SEO optimization, affiliate marketing, outdated guides, and corporate promotional copy masquerading as objective comparison. | High for factual specifications and release notes. Low for comparative evaluations or unverified product benchmarks. |
| `twitter` | Immediate real-time reactions, breaking developments, security vulnerability disclosures, conference and release announcements. | Severe recency and outrage bias; skews heavily toward extremes, hyperbole, and tribal polarization; quiet, measured analysis is drowned out; short character limits destroy technical nuance. | High for real-time awareness and breaking incident alerts. Low for balanced technical appraisals or library evaluations. |
| `linkedin` | Verifiable employment history, official organizational milestones, corporate hierarchy, public staffing announcements. | Pervasive performative optimism and professional self-censorship; virtually nobody writes anything negative about their current employer or recent employers due to career and reputational risks; corporate cheerleading. | High for verifying career tenure and official corporate announcements. Zero for candid workplace culture, executive criticism, or internal morale. |

---

## Worked example 1: Workplace culture

### The question

> *"How is it working at Company X? What is the engineering culture and management like?"*

### Trust evaluation and source routing

1. **Evaluate platform incentives:**
   Answering a question about workplace culture requires candid, unvarnished
   accounts of management behavior, engineering practices, on-call burdens, and
   internal politics.

2. **Examine `linkedin`:**
   LinkedIn is the natural place people look for company information, but its
   incentive model actively punishes candor. Current employees risk termination
   or career damage by posting negative feedback. Former employees avoid burning
   bridges with future hiring managers. Content on LinkedIn is performative and
   brand-protective.
   **Decision: Route EXPLICITLY AWAY from `linkedin`.**
   Consulting LinkedIn for workplace culture yields cheerleading and sanitized
   press releases, distorting the answer toward uniform positivity.

3. **Examine candid technical communities (`hacker-news`, `lobsters`):**
   Hacker News and Lobsters allow pseudonymous accounts and host frequent
   discussions on company layoffs, return-to-office mandates, engineering blog
   reactions, and "Ask HN" career retrospectives. Engineers frequently share
   unvarnished first-hand experiences about on-call rotations, tech debt, and
   management styles at well-known companies.
   **Decision: Route toward `hacker-news` and `lobsters`.**

4. **Examine `github`:**
   If Company X maintains open-source projects, public issue discussions and pull
   request reviews reveal how company engineers interact under stress, how
   outside contributions are handled, and how technical disputes are resolved.
   **Decision: Route toward `github` if the company maintains public repositories.**

### Resulting route decision

- **Chosen sources:** `hacker-news`, `lobsters`, `github` (if open-source repos exist)
- **Excluded source:** `linkedin`
- **Routing rationale:** "Workplace culture questions require candid accounts of internal engineering practices. LinkedIn is systematically distorted by professional self-censorship and PR cheerleading, where negative experiences are almost never posted. Hacker News and Lobsters provide pseudonymous accounts of engineering conditions and postmortems, while GitHub issue threads reflect real collaborative demeanor on public codebases."

---

## Worked example 2: Library and tool quality

### The question

> *"Is library Y any good? Should we adopt it in production?"*

### Trust evaluation and source routing

1. **Evaluate platform incentives:**
   Assessing a software library requires understanding production stability,
   unreported edge cases, concurrency hazards, breaking change discipline, and
   maintainer responsiveness.

2. **Examine `twitter`:**
   Microblog platforms thrive on viral engagement, release announcements, and
   enthusiastic "hot takes". A library may trend on Twitter because an influencer
   posted a flashy benchmark or a hype thread ("Library Y is 10x faster!").
   Twitter threads lack reproduction steps, fail to discuss architectural edge
   cases, and favor novelty over boring production stability.
   **Decision: Route EXPLICITLY AWAY from `twitter`.**
   Evaluating a library on Twitter risks confusing social momentum with software
   reliability.

3. **Examine `github` issues:**
   GitHub issues document the exact failure modes production users have
   encountered: memory leaks, race conditions, compatibility breaks, and unhelpful
   error messages. It also reveals whether the maintainers acknowledge bugs and
   merge fixes promptly.
   *Caveat:* Weigh with the understanding that only users with problems post
   issues.
   **Decision: Route toward `github`.**

4. **Examine technical discussion (`hacker-news`, `lobsters`):**
   When a library is discussed on Hacker News or Lobsters (e.g. "Show HN" threads,
   migration postmortems, or comparison benchmarks), experienced practitioners
   frequently debate alternatives, API ergonomical shortcomings, and real-world
   operational trade-offs at scale.
   **Decision: Route toward `hacker-news` and `lobsters`.**

### Resulting route decision

- **Chosen sources:** `github`, `hacker-news`, `lobsters`
- **Excluded source:** `twitter`
- **Routing rationale:** "Evaluating library quality requires concrete bug reports, maintainer responsiveness records, and deep architectural critiques. Twitter skews heavily toward novelty, promotional threads, and hype, obscuring production edge cases. GitHub issues expose real operational defects and triage discipline, while Hacker News and Lobsters provide in-depth comparative evaluations from engineers running systems at scale."

---

## User overrides and evidence recording

A user or operator may always override the routing skill's recommendation. For
example, a user evaluating a library may deliberately wish to monitor Twitter
hype to gauge developer community buzz, or a user researching Company X may want
to inspect official executive announcements on LinkedIn.

**A user override must never be silent.**

When an override occurs:
1. The fetch operation records `routing_override: true` in the evidence record.
2. The user-supplied rationale is recorded in `routing_override_reason`.
3. The override is preserved in the append-only evidence store and visible on
   the CLI stderr and MCP responses.
4. An overridden fetch is permanently distinguishable from a fetch recommended
   by the routing skill.

### CLI override invocation

```bash
# Override routing recommendations with an explicit reason
words-on-the-street fetch --override --override-reason "User wanted to check immediate Twitter reaction for product launch" twitter "launch announcement"
```

The CLI outputs:
- Stderr: `record: <hash>` and `override: true (reason: ...)`
- Stdout: Clean payload bytes as received, byte-for-byte unchanged.

### MCP tool override invocation

```json
{
  "name": "fetch",
  "arguments": {
    "url": "https://x.com/search?q=acme&f=live",
    "backend": "curl",
    "routing_override": true,
    "routing_override_reason": "Auditing social sentiment despite routing recommendation"
  }
}
```

---

## Output contract for synthesized answers

Stage four of Words on the Street is **Synthesis** (planned for milestone 10).
While individual fetches retrieve raw evidence records, any agent or subsystem
that synthesizes an answer from multiple fetches must expose the routing
decisions in its output.

### The synthesis contract

Every synthesized answer MUST include:

1. **`chosen_sources`:** The list of sources consulted and the explicit
   reasoning for each based on the routing model.
2. **`excluded_sources`:** The list of sources deliberately avoided, along with
   the specific bias or distortion that warranted their exclusion.
3. **`routing_override`:** A boolean indicating whether any source was included
   against the routing skill's recommendation, with the associated reason.
4. **`evidence_records`:** The content hash (`record: <hash>`) and resolved URL
   for every factual claim made in the synthesis.
5. **The three project limits:**
   - What is proved: bytes, time, hash, backend.
   - What is not proved: whether the bytes were true or whether the platform
     served the same to anyone else.
   - Non-representativeness: posters are not people; issue reporters are not the
     user base; professional networks contain no criticism.

### Output contract schema (JSON)

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "title": "SynthesisAnswer",
  "type": "object",
  "required": [
    "question",
    "chosen_sources",
    "excluded_sources",
    "routing_override",
    "claims",
    "attribution_records",
    "answer",
    "limitations"
  ],
  "properties": {
    "question": {
      "type": "string",
      "description": "The user query being answered."
    },
    "chosen_sources": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["source", "reason"],
        "properties": {
          "source": { "type": "string" },
          "reason": { "type": "string" }
        }
      },
      "description": "Sources selected and the rationale based on the routing trust model."
    },
    "excluded_sources": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["source", "reason"],
        "properties": {
          "source": { "type": "string" },
          "reason": { "type": "string" }
        }
      },
      "description": "Sources deliberately excluded and the bias reason."
    },
    "routing_override": {
      "type": "object",
      "required": ["is_overridden"],
      "properties": {
        "is_overridden": { "type": "boolean" },
        "reason": { "type": "string" }
      }
    },
    "claims": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["claim", "record_hash"],
        "properties": {
          "claim": { "type": "string" },
          "record_hash": { "type": "string" },
          "source": { "type": "string" }
        }
      }
    },
    "attribution_records": {
      "type": "array",
      "items": {
        "type": "string",
        "pattern": "^[a-f0-9]{64}$"
      },
      "description": "List of record hashes backing the claims."
    },
    "answer": {
      "type": "string",
      "description": "Synthesized response prose where every claim is attributed."
    },
    "limitations": {
      "type": "string",
      "description": "Standard statement of the three project limits and representativeness caveats."
    }
  }
}
```

### Output contract markdown template

When rendered in prose or Markdown by an agent, the output follows this structure:

```markdown
### Question
How is it working at Company X?

### Sources and Routing
- **Consulted:**
  - `hacker-news`: Candid engineering discussions and career postmortems from pseudonymous technical peers.
  - `lobsters`: Curated systems and engineering culture discussions.
- **Excluded:**
  - `linkedin`: Excluded because professional network incentives prevent negative or critical feedback about current and recent employers.
- **Routing override:** None (recommended routing followed).

### Answer
Based on 4 forum discussions recorded between January and August:
- Engineering compensation and benefits are reported as competitive (record: `a1b2c3...`).
- Multiple engineers cite high on-call pager load in the infrastructure team (record: `d4e5f6...`).
- Management communication during reorganization was criticized as opaque (record: `7890ab...`).

### Provenance and Limits
- Evidence verified: 3 records recorded in local append-only store.
- Limits: These records prove that these exact bytes were returned from these URLs at the recorded timestamps by the specified backends. They do not prove that the posters' statements were truthful or that the sample represents the majority of employees.
```
