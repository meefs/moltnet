import { fireEvent, render, screen, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { RowStateProvider, useRowFlag } from "../components/Content/Timeline/rowState";
import { continuesFrom, rowKeyOf, startsDay, TimelineMessage } from "../components/Content/Timeline/TimelineMessage";
import { prependRestoreTop } from "../components/Content/Timeline/scroll";
import { dayLabel } from "../lib/format";
import type { Message, MessagePart } from "../lib/types";

beforeEach(() => {
  vi.stubGlobal("fetch", vi.fn(async () => Response.json({ id: "n", name: "N", version: "1", capabilities: {}, console: { viewer: { kind: "anonymous" } } })));
});

function message(parts: MessagePart[], overrides: Partial<Message> = {}): Message {
  return { from: { type: "agent", id: "brass" }, target: { kind: "room", room_id: "desk" }, parts, created_at: "2026-10-25T12:00:00Z", ...overrides };
}

/** Renders rows inside one provider; `rerender` with a subset simulates virtualization. */
function timeline(rows: Message[]) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const view = (shown: Message[]) => (
    <QueryClientProvider client={client}>
      <RowStateProvider>
        {shown.map((row, index) => (
          <div key={index} data-testid={`row-${rows.indexOf(row)}`}>
            <TimelineMessage message={row} />
          </div>
        ))}
      </RowStateProvider>
    </QueryClientProvider>
  );
  const result = render(view(rows));
  return { ...result, show: (shown: Message[]) => result.rerender(view(shown)) };
}

describe("row state survives virtualization", () => {
  it("keeps a card in raw json after its row unmounts", () => {
    const row = message([{ kind: "data", data: { kind: "x.v1", a: 1 } }], { id: "m1" });
    const { show } = timeline([row]);
    fireEvent.click(screen.getByRole("button", { name: "raw json" }));
    show([]);
    show([row]);
    expect(screen.getByRole("button", { name: "table" })).toBeTruthy();
  });

  it("keeps consent to load an image or audio clip after its row unmounts", async () => {
    const row = message([{ kind: "image", url: "https://cdn.example/a.png" }, { kind: "audio", url: "https://cdn.example/a.mp3" }], { id: "m1" });
    const { container, show } = timeline([row]);
    fireEvent.click(await screen.findByRole("button", { name: /load image/ }));
    fireEvent.click(screen.getByRole("button", { name: /load audio/ }));
    show([]);
    show([row]);
    expect(container.querySelector("img")).toBeTruthy();
    expect(container.querySelector("audio")).toBeTruthy();
  });

  it("keeps a failed load failed after its row unmounts", async () => {
    const row = message([{ kind: "image", url: "https://cdn.example/broken.png" }], { id: "m1" });
    const { container, show } = timeline([row]);
    fireEvent.click(await screen.findByRole("button", { name: /load image/ }));
    fireEvent.error(container.querySelector("img")!);
    show([]);
    show([row]);
    expect(screen.getByText(/could not load/)).toBeTruthy();
  });

  it("never shares consent between messages, even without ids and with equal sender and time", async () => {
    const a = message([{ kind: "image", url: "https://a.example/a.png" }]);
    const b = message([{ kind: "image", url: "https://b.example/b.png" }]);
    expect(rowKeyOf(a)).not.toBe(rowKeyOf(b));
    const { container } = timeline([a, b]);
    fireEvent.click(await within(screen.getByTestId("row-0")).findByRole("button", { name: /load image/ }));
    expect(container.querySelectorAll("img")).toHaveLength(1);
    expect(within(screen.getByTestId("row-1")).getByRole("button", { name: /load image/ })).toBeTruthy();
  });

  it("never carries consent into another room", async () => {
    const here = message([{ kind: "image", url: "https://a.example/a.png" }], { id: "m1" });
    const there = message([{ kind: "image", url: "https://a.example/a.png" }], { id: "m1", target: { kind: "room", room_id: "other" } });
    const { container, show } = timeline([here, there]);
    show([here]);
    fireEvent.click(await screen.findByRole("button", { name: /load image/ }));
    show([there]);
    expect(container.querySelector("img")).toBeNull();
  });

  it("re-renders only the row whose flag changed", () => {
    const renders = { a: 0, b: 0 };
    function Flag({ name }: { name: "a" | "b" }) {
      const [on, set] = useRowFlag(name);
      renders[name] += 1;
      return <button onClick={() => set(!on)}>{name}</button>;
    }
    render(<RowStateProvider><Flag name="a" /><Flag name="b" /></RowStateProvider>);
    const before = { ...renders };
    fireEvent.click(screen.getByText("a"));
    expect(renders.a).toBe(before.a + 1);
    expect(renders.b).toBe(before.b);
  });
});

function at(created_at: string): Message {
  return message([], { created_at });
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

  it("keeps the row's bottom edge where it was, so changes at its top cannot move the text", () => {
    // The row's bottom was 140px below the viewport top; it now ends at 1370.
    const top = prependRestoreTop(node(), { scrollTop: 300, scrollHeight: 1000, prepend: { rowKey: "k", endOffset: 140 } }, 1370);
    expect(top).toBe(1230);
  });

  it("falls back to the height delta without an anchor position", () => {
    expect(prependRestoreTop(node(), { scrollTop: 300, scrollHeight: 1000, prepend: null }, undefined)).toBe(1300);
    expect(prependRestoreTop(node(), { scrollTop: 300, scrollHeight: 1000, prepend: { rowKey: "k", endOffset: 0 } }, undefined)).toBe(1300);
  });
});
