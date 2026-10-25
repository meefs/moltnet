# Landing Components Guide

Sections of the moltnet.dev landing page (`src/pages/index.astro`), in the
Noopolis visual system (`src/styles/landing*.css`, tokens in `tokens.css`).

- Markup only; behavior lives in `src/scripts/landing/`.
- Copy that the scripts also use (setup prompt, terminal script) lives in
  `src/scripts/landing/content.ts` — import it, never duplicate it.
- Keep facts in sync with the docs; change presentation here, not claims.
