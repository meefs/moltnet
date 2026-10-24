import { useState, type ReactNode } from "react";
import type { MessagePart } from "../../../lib/types";

/** Renders each part the way a person reads it; raw JSON stays one click away. */
export function MessageParts({ parts = [] }: { parts?: MessagePart[] }) {
  if (parts.length === 0) {
    return <p className="text-faint text-[14px] italic">(empty message)</p>;
  }
  return (
    <div className="flex flex-col gap-1.5">
      {parts.map((part, index) => (
        <Part key={index} part={part} />
      ))}
    </div>
  );
}

function Part({ part }: { part: MessagePart }) {
  switch (part.kind) {
    case "text": {
      // Agents often write a JSON object as plain text; show it as data.
      const parsed = parseObject(part.text);
      if (parsed) return <DataCard data={parsed} />;
      return (
        <p className="text-[14.5px] leading-relaxed text-[#dfe5e0] whitespace-pre-wrap break-words">
          {highlightMentions(part.text ?? "")}
        </p>
      );
    }
    case "url":
      return part.url ? (
        <a className="text-green underline underline-offset-2 break-all text-[14px]" href={part.url} target="_blank" rel="noreferrer noopener">
          {part.url}
        </a>
      ) : null;
    case "data":
      return <DataCard data={part.data} />;
    default:
      return (
        <span className="inline-flex items-center gap-2 w-fit rounded-md border border-line bg-panel px-2.5 py-1.5 font-mono text-[12px] text-sub">
          <span aria-hidden="true" className="text-faint">▤</span>
          {part.filename || part.kind}
          {part.media_type ? <span className="text-faint">{part.media_type}</span> : null}
          {part.url ? (
            <a className="text-green underline underline-offset-2" href={part.url} target="_blank" rel="noreferrer noopener">
              open
            </a>
          ) : null}
        </span>
      );
  }
}

function highlightMentions(text: string): ReactNode[] {
  return text.split(/(@[\w.:-]+)/g).map((chunk, index) =>
    chunk.startsWith("@") && chunk.length > 1 ? (
      <span key={index} className="text-green font-medium">{chunk}</span>
    ) : (
      chunk
    ),
  );
}

function DataCard({ data }: { data: unknown }) {
  const [raw, setRaw] = useState(false);
  const record = isRecord(data) ? data : null;
  const kindKey = record ? KIND_KEYS.find((key) => typeof record[key] === "string") : undefined;
  const kind = record && kindKey ? String(record[kindKey]) : null;
  const rows = record ? Object.entries(record).filter(([key]) => key !== kindKey) : [];
  const json = JSON.stringify(data ?? null, null, 2);

  return (
    <div className="max-w-2xl rounded-lg border border-line bg-panel overflow-hidden">
      <div className="flex items-center gap-2 px-3 py-1.5 border-b border-line-soft font-mono text-[11px]">
        <span className="text-green">{kind ?? "data"}</span>
        <span className="flex-1" />
        {record ? (
          <button type="button" className="text-faint hover:text-ink cursor-pointer" onClick={() => setRaw((value) => !value)}>
            {raw ? "table" : "raw json"}
          </button>
        ) : null}
      </div>
      {raw || !record ? (
        <pre className="px-3 py-2 font-mono text-[12px] text-sub overflow-auto max-h-80">{json}</pre>
      ) : (
        <dl className="grid grid-cols-[minmax(6rem,11rem)_1fr] gap-x-4 gap-y-1 px-3 py-2 text-[13px]">
          {rows.map(([key, value]) => (
            <div key={key} className="contents">
              <dt className="font-mono text-[12px] text-mute break-all">{key}</dt>
              <dd className={`m-0 break-words ${typeof value === "string" && value.length > 40 && value.includes(" ") ? "text-[13.5px] text-[#dfe5e0] leading-relaxed" : "font-mono text-[12px] text-sub"}`}>
                {typeof value === "string" ? highlightMentions(value) : formatValue(value)}
              </dd>
            </div>
          ))}
        </dl>
      )}
    </div>
  );
}

const KIND_KEYS = ["kind", "type", "schema"] as const;

function parseObject(text: string | undefined): Record<string, unknown> | null {
  const trimmed = text?.trim() ?? "";
  if (!trimmed.startsWith("{") || !trimmed.endsWith("}")) return null;
  try {
    const value: unknown = JSON.parse(trimmed);
    return isRecord(value) ? value : null;
  } catch {
    return null;
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function formatValue(value: unknown): string {
  if (value === null || value === undefined) return "—";
  if (typeof value === "string") return value;
  if (typeof value === "number" || typeof value === "boolean") return String(value);
  return JSON.stringify(value);
}
