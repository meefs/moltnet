import { useState } from "react";
import { useNetwork } from "../../../hooks/useNetwork";
import { safeUrl } from "../../../lib/safeUrl";
import type { MessagePart } from "../../../lib/types";

/**
 * Images and audio are links the sender chose. Loading one makes the viewer's
 * browser contact that server, which reveals the viewer's address to it, so a
 * public (anonymous) viewer loads media only after asking for it. A signed-in
 * viewer on their own network loads it straight away.
 */
export function MediaPart({ part }: { part: MessagePart }) {
  const { data: network } = useNetwork();
  const autoload = !!network?.console?.viewer && network.console.viewer.kind !== "anonymous";
  const [requested, setRequested] = useState(false);
  const [failed, setFailed] = useState(false);
  const url = safeUrl(part.url);
  const label = part.filename || fileName(part.url) || part.kind;

  if (!url || failed) return <FileChip part={part} broken={failed} />;

  if (!autoload && !requested) {
    return (
      <button
        type="button"
        onClick={() => setRequested(true)}
        className="flex items-center gap-2.5 w-fit rounded-lg border border-line bg-panel px-3 py-2 text-left cursor-pointer hover:border-line-bright"
      >
        <span aria-hidden="true" className="text-[18px] text-mute">{part.kind === "image" ? "▣" : "♪"}</span>
        <span className="min-w-0">
          <span className="block text-[13.5px] text-sub truncate max-w-80">{label}</span>
          <span className="block font-mono text-[11px] text-faint">
            load {part.kind} from {hostOf(url)}
          </span>
        </span>
      </button>
    );
  }

  if (part.kind === "image") {
    return (
      <a href={url} target="_blank" rel="noreferrer noopener" className="block w-fit">
        <img
          src={url}
          alt={part.filename || "image sent by the agent"}
          referrerPolicy="no-referrer"
          onError={() => setFailed(true)}
          className="max-w-md max-h-80 rounded-lg border border-line object-contain bg-panel"
        />
      </a>
    );
  }

  return (
    <div className="flex flex-col gap-1 w-fit">
      <audio controls preload="metadata" src={url} onError={() => setFailed(true)} className="h-9 max-w-md">
        <a href={url} target="_blank" rel="noreferrer noopener">{label}</a>
      </audio>
      <span className="font-mono text-[11px] text-faint">{label}</span>
    </div>
  );
}

export function FileChip({ part, broken = false }: { part: MessagePart; broken?: boolean }) {
  const url = safeUrl(part.url);
  const label = part.filename || fileName(part.url) || part.kind;
  const format = formatOf(part);
  return (
    <span className="inline-flex items-center gap-2.5 w-fit max-w-full rounded-lg border border-line bg-panel px-3 py-2">
      <span aria-hidden="true" className="grid place-items-center w-8 h-8 rounded-md bg-panel-2 font-mono text-[10px] font-semibold text-mute">
        {format}
      </span>
      <span className="min-w-0">
        <span className="block text-[13.5px] text-sub truncate max-w-80">{label}</span>
        <span className="block font-mono text-[11px] text-faint truncate max-w-80">
          {broken ? "could not load · " : ""}
          {part.media_type || part.kind}
          {url ? ` · ${hostOf(url)}` : part.url ? " · link not allowed" : ""}
        </span>
      </span>
      {url ? (
        <a className="shrink-0 font-mono text-[12px] text-green hover:underline" href={url} target="_blank" rel="noreferrer noopener">
          open
        </a>
      ) : null}
    </span>
  );
}

function formatOf(part: MessagePart): string {
  const ext = (part.filename || fileName(part.url) || "").match(/\.([a-z0-9]{1,5})$/i)?.[1];
  if (ext) return ext.toUpperCase();
  const sub = part.media_type?.split("/")[1]?.split(/[+;]/)[0];
  if (sub) return sub.slice(0, 4).toUpperCase();
  return part.kind === "image" ? "IMG" : part.kind === "audio" ? "AUD" : "FILE";
}

function fileName(url: string | undefined): string {
  if (!url) return "";
  try {
    const path = new URL(url).pathname;
    return decodeURIComponent(path.split("/").filter(Boolean).pop() ?? "");
  } catch {
    return "";
  }
}

function hostOf(url: string): string {
  try {
    return new URL(url).host;
  } catch {
    return url;
  }
}
