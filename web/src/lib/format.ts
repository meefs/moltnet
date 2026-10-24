import type { MessagePart } from "./types";

const pad = (n: number) => String(n).padStart(2, "0");

export function textFromParts(parts: MessagePart[] = []): string {
  return parts
    .map((part) => {
      if (part.kind === "text") return part.text || "";
      if (part.kind === "url") return part.url || "";
      if (part.kind === "data") return JSON.stringify(part.data ?? {});
      return part.filename || part.url || part.media_type || "";
    })
    .filter(Boolean)
    .join("\n");
}

export function formatTimestamp(value: string | number | Date | undefined | null): string {
  if (!value) return "—";
  const d = new Date(value);
  if (Number.isNaN(d.valueOf())) return String(value);
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
}

/** Local calendar day of a timestamp, for grouping messages by day. */
export function dayKey(value: string | undefined | null): string {
  const d = new Date(value ?? "");
  if (Number.isNaN(d.valueOf())) return "";
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
}

/** "Today", "Yesterday", or a full date such as "Thu, 23 Oct 2026". */
export function dayLabel(value: string, now = new Date()): string {
  const key = dayKey(value);
  if (!key) return "unknown date";
  if (key === dayKey(now.toISOString())) return "Today";
  const yesterday = new Date(now);
  yesterday.setDate(now.getDate() - 1);
  if (key === dayKey(yesterday.toISOString())) return "Yesterday";
  return new Date(value).toLocaleDateString([], { weekday: "short", day: "numeric", month: "short", year: "numeric" });
}

export function formatClockUTC(d: Date): string {
  return `${pad(d.getUTCHours())}:${pad(d.getUTCMinutes())}:${pad(d.getUTCSeconds())} UTC`;
}

export function formatUptime(ms: number): string {
  const seconds = Math.floor(ms / 1000);
  return `${pad(Math.floor(seconds / 3600))}:${pad(Math.floor((seconds / 60) % 60))}:${pad(seconds % 60)}`;
}
