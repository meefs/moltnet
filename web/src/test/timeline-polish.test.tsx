import { fireEvent, render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { MessageParts } from "../components/Content/Timeline/MessageParts";
import { RowStateProvider } from "../components/Content/Timeline/rowState";
import { continuesFrom, startsDay } from "../components/Content/Timeline/TimelineMessage";
import { prependRestoreTop } from "../components/Content/Timeline/scroll";
import { dayLabel } from "../lib/format";
import type { Message, MessagePart } from "../lib/types";

beforeEach(() => {
  vi.stubGlobal("fetch", vi.fn(async () => Response.json({ id: "n", name: "N", version: "1", capabilities: {}, console: { viewer: { kind: "anonymous" } } })));
});

function Row({ parts, shown }: { parts: MessagePart[]; shown: boolean }) {
  return shown ? <MessageParts parts={parts} rowKey="m1" /> : null;
}

function harness(parts: MessagePart[]) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const view = (shown: boolean) => (
    <QueryClientProvider client={client}>
      <RowStateProvider>
        <Row parts={parts} shown={shown} />
      </RowStateProvider>
    </QueryClientProvider>
  );
  const result = render(view(true));
  return { ...result, scrollAway: () => result.rerender(view(false)), scrollBack: () => result.rerender(view(true)) };
}

describe("row state survives virtualization", () => {
  it("keeps a card in raw json after its row unmounts", () => {
    const { scrollAway, scrollBack } = harness([{ kind: "data", data: { kind: "x.v1", a: 1 } }]);
    fireEvent.click(screen.getByRole("button", { name: "raw json" }));
    scrollAway();
    scrollBack();
    expect(screen.getByRole("button", { name: "table" })).toBeTruthy();
  });

  it("keeps an anonymous viewer's consent to load an image", async () => {
    const { container, scrollAway, scrollBack } = harness([{ kind: "image", url: "https://cdn.example/a.png" }]);
    fireEvent.click(await screen.findByRole("button", { name: /load image/ }));
    expect(container.querySelector("img")).toBeTruthy();
    scrollAway();
    scrollBack();
    expect(container.querySelector("img")).toBeTruthy();
  });
});

function at(created_at: string): Message {
  return { from: { type: "agent", id: "brass" }, target: { kind: "room", room_id: "desk" }, parts: [], created_at };
}

describe("days", () => {
  it("starts a new day and a new header at local midnight", () => {
    const late = at(new Date(2026, 9, 23, 23, 58).toISOString());
    const early = at(new Date(2026, 9, 24, 0, 1).toISOString());
    expect(startsDay(late, early)).toBe(true);
    expect(continuesFrom(late, early)).toBe(false);
    expect(startsDay(undefined, late)).toBe(true);
    expect(startsDay(at(new Date(2026, 9, 24, 0, 0).toISOString()), early)).toBe(false);
  });

  it("labels today, yesterday, and older dates in full", () => {
    const now = new Date(2026, 9, 25, 12, 0);
    expect(dayLabel(new Date(2026, 9, 25, 1, 0).toISOString(), now)).toBe("Today");
    expect(dayLabel(new Date(2026, 9, 24, 23, 0).toISOString(), now)).toBe("Yesterday");
    expect(dayLabel(new Date(2026, 9, 20, 9, 0).toISOString(), now)).toMatch(/2026/);
  });
});

describe("prepend restore", () => {
  const node = () => {
    const element = document.createElement("div");
    Object.defineProperty(element, "scrollHeight", { configurable: true, value: 2000 });
    return element;
  };

  it("puts the row being read back where it was, from the virtualizer's position", () => {
    // The row now starts at 1270 in the list; it was 40px below the viewport top.
    const top = prependRestoreTop(node(), { scrollTop: 300, scrollHeight: 1000, anchor: { messageID: "m", offsetTop: 40 } }, 1270);
    expect(top).toBe(1230);
  });

  it("falls back to the height delta without an anchor position", () => {
    expect(prependRestoreTop(node(), { scrollTop: 300, scrollHeight: 1000, anchor: null }, undefined)).toBe(1300);
    expect(prependRestoreTop(node(), { scrollTop: 300, scrollHeight: 1000, anchor: { messageID: "m", offsetTop: 0 } }, undefined)).toBe(1300);
  });
});
