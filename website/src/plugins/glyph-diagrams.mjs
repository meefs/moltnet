// Renders <pre class="mermaid"> blocks in docs Markdown to glyphcss text at
// build time, so diagrams ship as plain monospace text with no client script.
import { renderGlyphDiagram } from '@glyphcss/diagrams';
import { renderGlyphSequence } from '@glyphcss/diagrams/sequence';
import { createHash } from 'node:crypto';

const BLOCK = /^<pre class="mermaid">\n?([\s\S]*?)\n?<\/pre>$/;
const ROLES = { '#38bdf8': 'g-node', '#94a3b8': 'g-edge' };
const FRAME = /([─-▟▲-◿·]+)/;
const FRAME_START = /^[─-▟▲-◿·]/;
// Column budgets, widest first. Each diagram is laid out at every budget and
// CSS container queries show the widest layout that fits its column.
export const DIAGRAM_BUDGETS = [96, 80, 64, 52, 40];
const GUTTER_CH = 4;

function decode(source) {
  return source.replace(/&lt;/g, '<').replace(/&gt;/g, '>').replace(/&quot;/g, '"').replace(/&amp;/g, '&');
}

function visible(row) {
  return decode(row.replace(/<[^>]+>/g, ''));
}

// Drops blank rows and the shared left margin so the text centres cleanly.
function trim(html) {
  const rows = html.split('\n');
  while (rows.length && !visible(rows[0]).trim()) rows.shift();
  while (rows.length && !visible(rows.at(-1)).trim()) rows.pop();
  const margin = Math.min(...rows.filter((row) => visible(row).trim()).map((row) => visible(row).search(/\S/)));
  return rows.map((row) => row.replace(new RegExp(`^ {${margin}}`), '').replace(/\s+$/, '')).join('\n');
}

// Splits node spans so box borders and label text can be styled apart.
function classify(html) {
  return html
    .replace(/<span style="color:(#[0-9a-f]{6})">/g, (match, color) => {
      if (!ROLES[color]) throw new Error(`glyphcss used an unmapped diagram color ${color}`);
      return `<span class="${ROLES[color]}">`;
    })
    .replace(/<span class="g-node">([^<]*)<\/span>/g, (match, text) =>
      text.split(FRAME).filter(Boolean)
        .map((part) => `<span class="${FRAME_START.test(part) ? 'g-frame' : 'g-label'}">${part}</span>`)
        .join(''));
}

async function render(source, width) {
  const options = { target: 'web', charset: 'box', color: 'css', width, overflow: 'expand' };
  const result = /^\s*sequenceDiagram\b/.test(source)
    ? await renderGlyphSequence(source, options)
    : await renderGlyphDiagram(source, options);
  if (!result.html) throw new Error('glyphcss returned no HTML for a diagram');
  const lost = result.report.ledger.filter((entry) => entry.code === 'label-dropped');
  if (lost.length || result.report.unroutable.length) {
    throw new Error(`glyphcss dropped part of a diagram at ${width} columns: ${lost.map((entry) => entry.message).join('; ') || 'unroutable edges'}`);
  }
  const html = trim(classify(result.html));
  const columns = Math.max(...html.split('\n').map((row) => [...visible(row)].length));
  return { html, columns };
}

// A sideways flowchart that overflows its budget is retried top-down.
async function layout(source, width) {
  const sideways = /^(\s*(?:flowchart|graph)\s+)(LR|RL)\b/;
  const first = await render(source, width);
  if (first.columns <= width || !sideways.test(source)) return first;
  return render(source.replace(sideways, '$1TB'), width).catch(() => first);
}

// Returns one <figure> holding a layout per distinct width, narrowest first.
export async function renderDiagram(source) {
  const layouts = [];
  for (const width of DIAGRAM_BUDGETS) {
    let candidate;
    try {
      candidate = await layout(source, width);
    } catch (error) {
      if (width === DIAGRAM_BUDGETS[0]) throw error;
      continue; // Too narrow for this diagram; a wider layout scrolls instead.
    }
    if (!layouts.some((known) => known.columns === candidate.columns)) layouts.push({ ...candidate, width });
  }
  // A layout wider than its own budget only helps when nothing else exists.
  const fitting = layouts.filter((current) => current.columns <= current.width);
  if (fitting.length) layouts.splice(0, layouts.length, ...fitting);
  layouts.sort((a, b) => a.columns - b.columns);
  const id = `glyph-${createHash('sha256').update(source).digest('hex').slice(0, 10)}`;
  // Equal specificity, so the widest matching rule (last) wins.
  const rules = layouts.slice(1).map((current) => {
    const pick = `[data-columns="${current.columns}"]`;
    return `@container (min-width: ${current.columns + GUTTER_CH}ch) { #${id} > pre:not(${pick}) { display: none; } #${id} > pre${pick} { display: block; } }`;
  });
  const variants = layouts.map((current) => `<pre data-columns="${current.columns}" style="--columns: ${current.columns + GUTTER_CH}">${current.html}</pre>`);
  return `<figure class="glyph-diagram" id="${id}" role="img" aria-label="Diagram">${variants.join('')}<style>${rules.join(' ')}</style></figure>`;
}

export function remarkGlyphDiagrams() {
  return async (tree) => {
    const pending = [];
    const visit = (node) => {
      if (node.type === 'html') {
        const match = node.value.trim().match(BLOCK);
        if (match) pending.push(renderDiagram(decode(match[1])).then((html) => { node.value = html; }));
      }
      node.children?.forEach(visit);
    };
    visit(tree);
    await Promise.all(pending);
  };
}
