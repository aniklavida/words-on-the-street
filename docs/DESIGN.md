# Design System Specification

## Overview

Words on the Street is a local research tool that monitors configured sources and synthesises what people are saying. It provides a local dashboard served directly by the Go binary (`internal/dashboard`). This document records the visual identity, brand decisions, and interface rules implemented in the product's local web UI, built in accordance with the project's design system.

## Brand Decisions

- **Product:** Words on the Street, a local research tool that watches sources and synthesises what people are saying, with a local dashboard served by the Go binary.
- **Audience:** One person reading on their own machine during long reading sessions.
- **Direction:** Editorial reading tool. Calm, restrained, legible, and focused on evidence.
- **Theme Defaults:** Light default, dark theme available. Persisted manual toggle in the navigation shell with fallback to `prefers-color-scheme`.
- **Vibe Words:** Thoughtful, honest, calm.

### The Five Brand Values

| Value | Setting | Description |
|---|---|---|
| **Hue** | `60` (ochre) | Warm, earthy ochre tone supporting long-form reading without fatigue. |
| **Chroma** | `0.10` | Restrained saturation, keeping focus on text and data. |
| **Warmth** | `0.012` | Subtle paper warmth in light mode and soft coal warmth in dark mode. |
| **Radius Scale** | `0.9` | Precision radius: 3.6px small, 9px inputs/buttons, 12.6px cards. |
| **Type Pairing** | System | Native system UI stack and system monospace stack. Zero font files shipped. |

### Typography and Assets

To ensure zero third-party requests, zero telemetry, and complete offline capability:
- **No external fonts:** System UI stack (`system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif`) and monospace stack (`ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace`).
- **No icon packs or hand-drawn logos:** The product name is a wordmark set in the display face.
- **Words first:** Clear typographic text labels are used in place of icons. Where an icon might otherwise be placed, descriptive text takes precedence.
- **No external images or brand logos:** Source badges and identity marks use neutral, deterministic initial avatars styled with system tokens.

### Honesty Framing

Evidence that misdescribes itself is worse than no evidence. The interface reflects this core tenet:
- **Tamper-evident status:** Ledger verification status is displayed clearly at the top of the dashboard.
- **Provability:** Indicators clearly distinguish verified content, fallback paths, and changed observations.
- **No hidden caveats:** Status caveats, error reasons, and reliability notes remain fully legible and prominent.

## Non-Negotiables

1. **One primary action per screen:** Primary button emphasis is reserved for the single main intent. All secondary actions use outline or ghost styles.
2. **Accent under 5%:** The ochre accent color is used sparingly for links, active indicators, and focus rings.
3. **Labels above inputs:** Labels sit directly above their respective fields.
4. **No decorative distractions:**
   - No gradient backgrounds.
   - No colored left borders.
   - No emoji in chrome or navigation.
   - No zebra-striped tables.
   - No modals for forms.
5. **Sentence case:** All headings, button labels, navigation links, and descriptions use sentence case.
6. **Every state designed:** Calm empty states, error states, and loading states are defined for every view.
7. **Accessibility & Contrast:** WCAG AA compliance (4.5:1 text contrast minimum in both light and dark themes) and clear keyboard focus rings.
