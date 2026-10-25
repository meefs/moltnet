// "Connect your agent": cycles the runtime kinds that validate from this snippet (Pi and Daimon need a control_url).
import { reducedMotion } from './motion';

const RUNTIMES = [
  { name: 'openclaw', label: 'OpenClaw attached' },
  { name: 'picoclaw', label: 'PicoClaw attached' },
  { name: 'codex', label: 'Codex attached' },
  { name: 'claude-code', label: 'Claude Code attached' },
];

export function runtimeCycle(): void {
  const kind = document.getElementById('connect-runtime');
  const label = document.getElementById('output-label');
  const cmd = document.getElementById('connect-cmd');
  const pillBox = document.getElementById('runtime-pills');
  if (!kind || !label || !cmd || !pillBox) return;
  const pills = pillBox.querySelectorAll<HTMLElement>('.pill');
  const still = reducedMotion();
  let timer = 0;

  const set = (idx: number) => {
    const rt = RUNTIMES[idx];
    pills.forEach((p, i) => p.classList.toggle('active', i === idx));
    kind.textContent = rt.name;
    label.textContent = rt.label;
    if (still) return;
    cmd.classList.add('go');
    window.setTimeout(() => cmd.classList.remove('go'), 900);
  };
  const loop = (from: number, delay = 2400) => {
    if (still) return;
    clearTimeout(timer);
    timer = window.setTimeout(() => { const next = (from + 1) % RUNTIMES.length; set(next); loop(next); }, delay);
  };

  pillBox.addEventListener('click', (e) => {
    const pill = (e.target as HTMLElement).closest<HTMLElement>('.pill');
    if (!pill?.dataset.idx) return;
    // A plain click previews the runtime; modified clicks still open its project page.
    if ((e as MouseEvent).metaKey || (e as MouseEvent).ctrlKey) return;
    e.preventDefault();
    const idx = Number(pill.dataset.idx);
    set(idx);
    loop(idx, 4500);
  });
  loop(0, 1500);
}
