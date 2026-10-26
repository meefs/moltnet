/**
 * Generates static icon assets from public/favicon.svg:
 * - favicon-32.png     (32x32, for <link rel="icon" sizes="32x32">)
 * - apple-touch-icon.png (180x180)
 *
 * Run via `npm run images`. Link-preview images are rendered at build time by
 * src/og/card.mjs instead (src/pages/og.png.ts and src/pages/og/).
 */

import sharp from 'sharp';
import { join } from 'path';

const PUBLIC = new URL('../public', import.meta.url).pathname;

const FAVICON = join(PUBLIC, 'favicon.svg');

await sharp(FAVICON).resize(32, 32).png().toFile(join(PUBLIC, 'favicon-32.png'));
await sharp(FAVICON).resize(180, 180).png().toFile(join(PUBLIC, 'apple-touch-icon.png'));

console.log('Generated favicon-32.png, apple-touch-icon.png');
