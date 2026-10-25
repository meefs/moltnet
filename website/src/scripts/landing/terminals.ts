// The hero loop: two terminals exchange one message over the text wire.
// Reduced motion renders the finished transcript once and never loops.
import { TERMINAL_HEADERS, TERMINAL_SCRIPT, type Line, type Side } from './content';
import { reducedMotion, sleep } from './motion';

const MAX_LINES = 11;

export function terminals(): void {
  const term = { A: document.getElementById('tA'), B: document.getElementById('tB') };
  const wire = document.getElementById('wire');
  if (!term.A || !term.B || !wire) return;
  const body = {
    A: term.A.querySelector<HTMLElement>('.body')!,
    B: term.B.querySelector<HTMLElement>('.body')!,
  };

  const add = ({ side, cls, html }: Line) => {
    const d = document.createElement('div');
    d.className = cls;
    d.innerHTML = html;
    body[side].appendChild(d);
    while (body[side].children.length > MAX_LINES) body[side].firstElementChild?.remove();
  };
  const reset = () => {
    (['A', 'B'] as Side[]).forEach((s) => {
      body[s].innerHTML = '';
      add({ side: s, cls: 'dim', html: TERMINAL_HEADERS[s] });
    });
  };

  if (reducedMotion()) {
    reset();
    TERMINAL_SCRIPT.forEach((s) => { if (!('send' in s)) add(s); });
    return;
  }

  const send = async (from: Side) => {
    const p = Object.assign(document.createElement('span'), { className: 'pkt', textContent: '◆' });
    wire.appendChild(p);
    const w = wire.clientWidth;
    p.style.left = (from === 'A' ? 0 : w - 10) + 'px';
    await sleep(30);
    p.style.left = (from === 'A' ? w - 10 : 0) + 'px';
    await sleep(1150);
    p.remove();
  };

  const loop = async () => {
    for (;;) {
      reset();
      await sleep(1200);
      for (const s of TERMINAL_SCRIPT) {
        if ('send' in s) { await send(s.send); continue; }
        term.A!.classList.toggle('lit', s.side === 'A');
        term.B!.classList.toggle('lit', s.side === 'B');
        add(s);
        await sleep(s.cls === 'nudge' ? 1300 : s.cls === 'dim' ? 700 : 1100);
      }
      await sleep(4000);
    }
  };
  void loop();
}
