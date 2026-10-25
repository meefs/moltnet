// Page chrome: GitHub star count, copy-to-clipboard buttons, and the toast.
import { COPIED_TOAST } from './content';

export function stars(): void {
  const els = document.querySelectorAll<HTMLElement>('[data-stars]');
  if (!els.length) return;
  fetch('https://api.github.com/repos/noopolis/moltnet')
    .then((r) => (r.ok ? r.json() : null))
    .then((v) => {
      if (v && typeof v.stargazers_count === 'number') {
        els.forEach((el) => (el.textContent = ' ' + v.stargazers_count.toLocaleString()));
      }
    })
    .catch(() => {});
}

export function copyButtons(): void {
  const toast = Object.assign(document.createElement('div'), { className: 'toast', role: 'status' });
  toast.setAttribute('aria-live', 'polite');
  document.body.appendChild(toast);
  let timer = 0;
  const show = (text: string) => {
    toast.textContent = text;
    toast.classList.add('on');
    clearTimeout(timer);
    timer = window.setTimeout(() => toast.classList.remove('on'), 2200);
  };
  document.querySelectorAll<HTMLElement>('[data-copy]').forEach((b) => {
    b.addEventListener('click', async () => {
      try {
        await navigator.clipboard.writeText(b.dataset.copy ?? '');
        show(b.dataset.toast ?? COPIED_TOAST);
      } catch {
        show('Your browser blocked copying. The Quickstart has the same steps.');
      }
    });
  });
}
