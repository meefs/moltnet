import { act, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Message, MoltnetEvent, Selection } from "../lib/types";

// Shared mutable state behind the mocked providers and hooks.
const state = vi.hoisted(() => ({
  events: [] as MoltnetEvent[],
  receipts: new Map<MoltnetEvent, number>(),
  latest: 0,
  selected: null as Selection | null,
  messages: [] as Message[],
  pinCalls: 0,
  observers: [] as Array<() => void>,
}));

vi.mock("../providers", () => ({
  useEventStream: () => ({ status: "open", events: state.events }),
  useSelection: () => ({ selected: state.selected, select: vi.fn() }),
  receiptOf: (event: MoltnetEvent) => state.receipts.get(event) ?? 0,
  latestReceipt: () => state.latest,
}));
vi.mock("../hooks/useNetwork", () => ({ useNetwork: () => ({ data: { id: "n", capabilities: {} } }) }));
vi.mock("../hooks/useRooms", () => ({ useRooms: () => ({ data: [] }) }));
vi.mock("../hooks/useDMs", () => ({ useDMs: () => ({ data: [] }) }));
vi.mock("../hooks/useMessages", () => ({
  useMessages: () => ({ data: { pages: [{ messages: state.messages }] }, hasNextPage: false, isFetchingNextPage: false }),
}));
vi.mock("../components/Content/Timeline/scroll", async (original) => ({
  ...(await original<typeof import("../components/Content/Timeline/scroll")>()),
  pinToBottom: () => {
    state.pinCalls += 1;
  },
}));

import { useUnread } from "../hooks/useUnread";
import { Timeline } from "../components/Content/Timeline/Timeline";

function receive(roomId: string, createdAt = "2000-01-01T00:00:00Z"): MoltnetEvent {
  const event: MoltnetEvent = {
    type: "message.created",
    created_at: createdAt,
    message: { from: { type: "agent", id: "a" }, target: { kind: "room", room_id: roomId }, parts: [], created_at: createdAt },
  };
  state.latest += 1;
  state.receipts.set(event, state.latest);
  state.events = [event, ...state.events];
  return event;
}

function Unread() {
  const counts = useUnread("room");
  return <p data-testid="unread">{JSON.stringify(Object.fromEntries(counts))}</p>;
}

beforeEach(() => {
  state.events = [];
  state.receipts.clear();
  state.latest = 0;
  state.selected = null;
  state.messages = [];
  state.pinCalls = 0;
  state.observers = [];
});

describe("useUnread", () => {
  it("counts by receipt order, ignoring server timestamps", () => {
    receive("a"); // arrived before the tab opened: never unread
    const { rerender } = render(<Unread />);
    receive("a", "1970-01-01T00:00:00Z"); // server clock far behind
    receive("b", "2999-01-01T00:00:00Z"); // server clock far ahead
    rerender(<Unread />);
    expect(screen.getByTestId("unread").textContent).toBe('{"b":1,"a":1}');
  });

  it("clears a channel while it is open and after leaving it", () => {
    const { rerender } = render(<Unread />);
    receive("a");
    state.selected = { kind: "room", id: "a" };
    rerender(<Unread />);
    expect(screen.getByTestId("unread").textContent).toBe("{}");
    receive("a");
    state.selected = { kind: "room", id: "b" };
    rerender(<Unread />);
    expect(screen.getByTestId("unread").textContent).toBe("{}");
    receive("a");
    rerender(<Unread />);
    expect(screen.getByTestId("unread").textContent).toBe('{"a":1}');
  });
});

describe("timeline follow", () => {
  it("re-pins the bottom when rows grow after loading", async () => {
    vi.stubGlobal(
      "ResizeObserver",
      class {
        private targets: Element[] = [];
        constructor(private callback: (entries: unknown[]) => void) {
          state.observers.push(() =>
            this.callback(this.targets.map((target) => ({
              target,
              contentRect: { width: 600, height: 400 },
              borderBoxSize: [{ inlineSize: 600, blockSize: 400 }],
            }))),
          );
        }
        observe(target: Element) {
          this.targets.push(target);
        }
        unobserve() {}
        disconnect() {}
      },
    );
    state.selected = { kind: "room", id: "desk" };
    state.messages = [{ id: "m1", from: { type: "agent", id: "a" }, target: { kind: "room", room_id: "desk" }, parts: [{ kind: "text", text: "hi" }], created_at: "2026-10-24T10:00:00Z" }];
    render(<Timeline />);
    // Let the initial open-at-bottom pins finish, so they cannot mask anything.
    await act(() => new Promise((done) => setTimeout(done, 150)));
    expect(state.observers.length).toBeGreaterThan(0);
    const before = state.pinCalls;
    act(() => state.observers.forEach((fire) => fire()));
    expect(state.pinCalls).toBeGreaterThan(before);

    // The pin's own scroll event lands while the rows are still off the bottom:
    // it must not count as the operator scrolling away.
    const scroller = screen.getByTestId("timeline-scroll");
    Object.defineProperty(scroller, "scrollHeight", { configurable: true, value: 1000 });
    Object.defineProperty(scroller, "clientHeight", { configurable: true, value: 300 });
    scroller.scrollTop = 0;
    act(() => {
      scroller.dispatchEvent(new Event("scroll"));
    });
    const afterScroll = state.pinCalls;
    act(() => state.observers.forEach((fire) => fire()));
    expect(state.pinCalls).toBeGreaterThan(afterScroll);
  });
});
