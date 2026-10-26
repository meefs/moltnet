# Link-Preview Cards Guide

`card.mjs` renders the 1200x630 link-preview images (og:image, twitter:image)
at build time with satori and resvg, in Geist from the `geist` package.

- `src/pages/og.png.ts` serves the landing card at `/og.png`.
- `src/pages/og/[...slug].png.ts` serves one card per docs page at
  `/og/<page id>.png`, from the page's title and description.
- `src/routeData.ts` adds those images to each docs page's head.
- Colors follow `src/styles/tokens.css`. Satori supports a subset of CSS;
  every element is a flex box. Check a rendered card after changing a layout.
