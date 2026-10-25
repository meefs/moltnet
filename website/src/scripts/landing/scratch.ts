// A pen crossing a word out: fast back-and-forth strokes with sharp turnarounds.
// perfect-freehand (tldraw's ink) turns the gesture into a pressure-varying
// outline, thin where the pen is fast and heavy at the turns; an SVG mask
// animates stroke-dashoffset along the gesture's spine to reveal it.
import { getStroke } from 'perfect-freehand';

type Pt = [number, number];

function gesture(w: number, h: number): Pt[] {
  const r = (lo: number, hi: number) => lo + Math.random() * (hi - lo);
  const pts: Pt[] = [];
  const strokes = 4, top = h * 0.42, bot = h * 0.84;
  for (let k = 0; k < strokes; k++) {
    // consecutive strokes slope opposite ways so they cross, like an impatient marker scribble
    const ltr = k % 2 === 0;
    const x0 = ltr ? r(-0.05, 0.02) * w : r(0.97, 1.06) * w;
    const x1 = ltr ? r(0.96, 1.06) * w : r(-0.06, 0.02) * w;
    const band = (bot - top) * r(0.55, 0.9), mid = top + (bot - top) * r(0.35, 0.65);
    const y0 = ltr ? mid - band / 2 : mid + band / 2, y1 = ltr ? mid + band / 2 : mid - band / 2;
    const bow = r(-0.08, 0.08) * h;
    for (let i = 0; i <= 22; i++) {
      const u = i / 22, t = 0.5 - Math.cos(u * Math.PI) / 2;
      pts.push([x0 + (x1 - x0) * t + r(-0.8, 0.8), y0 + (y1 - y0) * t + Math.sin(t * Math.PI) * bow + r(-0.8, 0.8)]);
    }
    pts.push([x1 + (ltr ? 1 : -1) * r(3, 8), y1 + r(-4, 4)]);
  }
  return pts;
}

function outlinePath(outline: number[][]): string {
  const f = (v: number) => v.toFixed(1);
  const parts = ['M', f(outline[0][0]), f(outline[0][1]), 'Q'];
  outline.forEach(([x0, y0], i, a) => {
    const [x1, y1] = a[(i + 1) % a.length];
    parts.push(f(x0), f(y0), f((x0 + x1) / 2), f((y0 + y1) / 2));
  });
  return parts.join(' ') + ' Z';
}

function draw(el: HTMLElement): void {
  const w = el.offsetWidth, h = el.offsetHeight;
  if (!w || !h) return;
  const pts = gesture(w, h);
  const size = Math.max(5, h * 0.13);
  const outline = getStroke(pts, {
    size, thinning: 0.5, smoothing: 0.55, streamline: 0.3, simulatePressure: true,
    start: { taper: size * 2 }, end: { taper: size * 3 },
  });
  if (!outline.length) return;
  const spine = 'M' + pts.map(([x, y]) => `${x.toFixed(1)} ${y.toFixed(1)}`).join(' L');
  const id = 'm' + Math.random().toString(36).slice(2, 8);
  el.querySelector('svg.ink')?.remove();
  el.insertAdjacentHTML('beforeend',
    `<svg class="ink" width="${w}" height="${h}" viewBox="0 0 ${w} ${h}" aria-hidden="true">` +
    `<mask id="${id}" maskUnits="userSpaceOnUse"><path class="reveal" d="${spine}" pathLength="1" stroke-width="${size * 2.4}"/></mask>` +
    `<path d="${outlinePath(outline)}" mask="url(#${id})"/></svg>`);
  el.classList.add('inked');
}

export async function scratch(el: HTMLElement): Promise<void> {
  await document.fonts.ready;
  draw(el);
  // The word's box changes with the viewport; redraw to fit, already revealed.
  let last = `${el.offsetWidth}x${el.offsetHeight}`;
  new ResizeObserver(() => {
    const now = `${el.offsetWidth}x${el.offsetHeight}`;
    if (now === last) return;
    last = now;
    el.classList.add('drawn');
    draw(el);
  }).observe(el);
}
