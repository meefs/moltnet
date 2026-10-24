/**
 * Links in a message are chosen by the sender. Only plain web links are ever
 * rendered as clickable or loadable; anything else (javascript:, data:, file:)
 * is shown as text.
 */
export function safeUrl(value: string | undefined): string | null {
  const trimmed = value?.trim();
  if (!trimmed) return null;
  try {
    const url = new URL(trimmed);
    return url.protocol === "https:" || url.protocol === "http:" ? url.href : null;
  } catch {
    return null;
  }
}
