# Website Plugins Guide

Build-time Markdown plugins wired in `astro.config.mjs`.

- `glyph-diagrams.mjs`: renders `<pre class="mermaid">` blocks in docs pages to
  glyphcss text (`@glyphcss/diagrams`) at build time. No client script ships.
  Colors map to `.g-frame`, `.g-label` and `.g-edge` in `src/styles/custom.css`;
  an unmapped glyphcss color, or a layout that drops a label or edge, fails the
  build (narrower layouts that lose content are skipped) instead of shipping.
- Each diagram is laid out at every width in `DIAGRAM_BUDGETS`; container queries
  show the widest that fits, so it reflows from desktop to phone. Check the build
  output at desktop and phone width after editing
  one, since glyphcss may wrap labels, flip orientation or expand the canvas.
