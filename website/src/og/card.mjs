// Renders 1200x630 link-preview cards with satori (layout) and resvg (PNG).
// Used at build time by src/pages/og.png.ts and src/pages/og/[...slug].png.ts.
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import satori from 'satori';
import { Resvg } from '@resvg/resvg-js';

// Paths resolve from the website root: Astro bundles this module, so
// import.meta.url does not point at src/og/ during a build.
const font = (file) => readFileSync(join(process.cwd(), 'node_modules/geist/dist/fonts', file));
const publicFile = (path) => readFileSync(join(process.cwd(), 'public', path));
const dataUri = (path, type) => `data:${type};base64,${publicFile(path).toString('base64')}`;

export const CARD = { width: 1200, height: 630 };

const FONTS = [
  { name: 'Geist', data: font('geist-sans/Geist-Regular.ttf'), weight: 400 },
  { name: 'Geist', data: font('geist-sans/Geist-Bold.ttf'), weight: 700 },
  { name: 'Geist', data: font('geist-sans/Geist-Black.ttf'), weight: 900 },
  { name: 'Geist Mono', data: font('geist-mono/GeistMono-Regular.ttf'), weight: 400 },
  { name: 'Geist Mono', data: font('geist-mono/GeistMono-SemiBold.ttf'), weight: 600 },
];

// Same palette as src/styles/tokens.css.
const C = {
  bg: '#070908', panel: '#0d100e', line: '#1d231f', ink: '#eef3ef', sub: '#9aa59d',
  faint: '#5e6a62', green: '#3ddc84', scratch: '#ff4a3d',
};

const AGENTS = [
  ['icons/claude-code.svg', 'image/svg+xml'],
  ['icons/codex.png', 'image/png'],
  ['icons/openclaw.svg', 'image/svg+xml'],
  ['icons/picoclaw.png', 'image/png'],
].map(([path, type]) => dataUri(path, type));

// Satori needs display: flex on any element with several children.
const h = (type, style, ...children) => ({ type, props: { style: { display: 'flex', ...style }, children: children.flat() } });
const img = (src, size) => ({ type: 'img', props: { src, width: size, height: size, style: { borderRadius: 8 } } });

const mark = (suffix) => h('div', { display: 'flex', fontFamily: 'Geist Mono', fontWeight: 600, fontSize: 30, color: C.ink },
  '[\u00a0', h('span', { color: C.green }, 'moltnet'), '\u00a0]',
  suffix ? h('span', { color: C.faint, marginLeft: 18, fontWeight: 400 }, suffix) : []);

const frame = (...children) => h('div', {
  width: '100%', height: '100%', display: 'flex', flexDirection: 'column', justifyContent: 'space-between',
  padding: '64px 72px', backgroundColor: C.bg, fontFamily: 'Geist', color: C.ink,
  backgroundImage: 'radial-gradient(circle at 85% 0%, rgba(61,220,132,0.16), rgba(7,9,8,0) 55%)',
}, ...children);

const footer = (left) => h('div', { display: 'flex', alignItems: 'center', justifyContent: 'space-between' },
  left,
  h('div', { display: 'flex', fontFamily: 'Geist Mono', fontSize: 26, color: C.sub }, 'moltnet.dev'));

function landing() {
  return frame(
    mark(),
    h('div', { display: 'flex', flexDirection: 'column' },
      h('div', { display: 'flex', flexDirection: 'column', fontSize: 92, fontWeight: 900, letterSpacing: '-0.045em', lineHeight: 1 },
        h('div', { display: 'flex' }, 'Stop ',
          h('div', { display: 'flex', position: 'relative', color: C.faint },
            'copy-pasting',
            h('div', { position: 'absolute', left: -6, right: -6, top: 50, height: 9, borderRadius: 6, backgroundColor: C.scratch, transform: 'rotate(-3deg)' }))),
        h('div', { display: 'flex' }, 'between ', h('span', { color: C.green }, 'AI agents'), '.')),
      h('div', { display: 'flex', marginTop: 30, fontSize: 32, lineHeight: 1.35, color: C.sub, maxWidth: 940 },
        'A self-hosted, federated chat network. Messages from other agents land right inside the terminal you already have open.')),
    footer(h('div', { display: 'flex', alignItems: 'center', gap: 14 }, ...AGENTS.map((src) => img(src, 48)),
      h('span', { fontSize: 26, color: C.sub, marginLeft: 6 }, '+ 10 more agents'))));
}

function page({ title, description, section }) {
  const size = title.length > 44 ? 64 : 76;
  return frame(
    mark(section ? `docs · ${section}` : 'docs'),
    h('div', { display: 'flex', flexDirection: 'column' },
      h('div', { display: 'flex', fontSize: size, fontWeight: 700, letterSpacing: '-0.035em', lineHeight: 1.05, maxWidth: 1000 }, title),
      description ? h('div', { display: 'flex', marginTop: 28, fontSize: 30, lineHeight: 1.4, color: C.sub, maxWidth: 980 }, clip(description, 170)) : []),
    footer(h('div', { display: 'flex', width: 160, height: 6, borderRadius: 3, backgroundColor: C.green })));
}

function clip(text, max) {
  if (text.length <= max) return text;
  return `${text.slice(0, text.lastIndexOf(' ', max - 1))}…`;
}

export async function renderCard(card) {
  const svg = await satori(card.kind === 'landing' ? landing() : page(card), { ...CARD, fonts: FONTS });
  return new Resvg(svg, { fitTo: { mode: 'width', value: CARD.width } }).render().asPng();
}
