# Website Scripts Guide

Client-side scripts bundled by Astro.

- `mermaid.js`: renders Mermaid diagrams on docs pages (injected site-wide).
- `landing/`: landing-page behavior, including the hero scratch (perfect-freehand,
  bundled from npm), the terminal loop, copy buttons, and the star count.
- Every animation must have a static `prefers-reduced-motion` path.
- No CDN imports; add runtime dependencies to `package.json`.
