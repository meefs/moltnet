export const SCROLL_TOP_FETCH_PX = 120;
export const SCROLL_BOTTOM_STICKY_PX = 32;

export interface ScrollSnapshot {
  scrollTop: number;
  scrollHeight: number;
  /** The row the operator was looking at, when one was visible. */
  anchor?: ScrollAnchor | null;
}

export interface ScrollAnchor {
  messageID: string;
  offsetTop: number;
}

export function isNearTop(node: HTMLElement) {
  return node.scrollTop <= SCROLL_TOP_FETCH_PX;
}

export function isNearBottom(node: HTMLElement) {
  return node.scrollHeight - node.scrollTop - node.clientHeight <= SCROLL_BOTTOM_STICKY_PX;
}

export function isTimelineAtBottom(node: HTMLElement) {
  return isNearBottom(node);
}

export function captureScrollSnapshot(node: HTMLElement): ScrollSnapshot {
  return {
    scrollTop: node.scrollTop,
    scrollHeight: node.scrollHeight,
  };
}

/**
 * Where to scroll after older messages load above the row the operator was
 * reading. `anchorStart` is that row's position in the virtualizer's own
 * measurements: an absolute answer that stays right while rows above are
 * still being measured, where DOM height deltas double-count the
 * virtualizer's own scroll corrections. The delta is only a fallback.
 */
export function prependRestoreTop(
  node: HTMLElement,
  snapshot: ScrollSnapshot,
  anchorStart: number | undefined,
): number {
  if (snapshot.anchor && anchorStart !== undefined) {
    return Math.max(0, anchorStart - snapshot.anchor.offsetTop);
  }
  return snapshot.scrollTop + Math.max(0, node.scrollHeight - snapshot.scrollHeight);
}

export function captureScrollAnchor(node: HTMLElement): ScrollAnchor | null {
  const container = node.getBoundingClientRect();
  let best: ScrollAnchor | null = null;
  let bestDistance = Number.POSITIVE_INFINITY;

  for (const item of node.querySelectorAll<HTMLElement>("[data-message-id]")) {
    const id = item.dataset.messageId;
    if (!id) continue;
    const rect = item.getBoundingClientRect();
    if (rect.bottom < container.top || rect.top > container.bottom) continue;
    const offsetTop = rect.top - container.top;
    const distance = Math.abs(offsetTop);
    if (distance < bestDistance) {
      best = { messageID: id, offsetTop };
      bestDistance = distance;
    }
  }

  return best;
}

export function restoreScrollAnchor(node: HTMLElement, anchor: ScrollAnchor) {
  for (const item of node.querySelectorAll<HTMLElement>("[data-message-id]")) {
    if (item.dataset.messageId !== anchor.messageID) continue;
    const offsetTop = item.getBoundingClientRect().top - node.getBoundingClientRect().top;
    node.scrollTop += offsetTop - anchor.offsetTop;
    return true;
  }
  return false;
}

export function pinToBottom(node: HTMLElement) {
  node.scrollTop = Math.max(0, node.scrollHeight - node.clientHeight);
}
