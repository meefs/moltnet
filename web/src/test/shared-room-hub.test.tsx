import { act, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { afterAll, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { Router } from "wouter";
import { memoryLocation } from "wouter/memory-location";
import { buildTimelineRows } from "../components/Content/Timeline/timelineRows";
import { Timeline } from "../components/Content/Timeline/Timeline";
import { TimelineHeader } from "../components/Content/Timeline/TimelineHeader";
import { MembersPanel } from "../components/Members/MembersPanel";
import { api } from "../lib/api";
import type { Message, RoomHub } from "../lib/types";
import { EventStreamProvider } from "../providers/EventStreamProvider";
import { SelectionProvider } from "../providers/SelectionProvider";

const FP = `sha256:${"ab".repeat(32)}`;

function hubFixture(overrides: Partial<RoomHub> = {}): RoomHub {
  return {
    global_room_id: "sha256:feed/research",
    local_room_id: "research",
    home_network_id: "newsroom",
    is_home: false,
    membership_epoch: 4,
    history: "from_join",
    members: [
      { network_id: "newsroom", role: "owner", fingerprint: FP, trust: "verified" },
      { network_id: "lab", role: "writer", fingerprint: FP, trust: "introduced" },
      { network_id: "me", role: "writer", fingerprint: FP, trust: "self" },
    ],
    events: [
      { seq: 1, epoch: 1, kind: "genesis", network_id: "newsroom", created_at: "2026-10-25T09:00:00Z" },
      { seq: 2, epoch: 2, kind: "add", network_id: "lab", role: "writer", created_at: "2026-10-25T10:30:00Z" },
      { seq: 3, epoch: 3, kind: "remove", network_id: "old", created_at: "2026-10-25T11:30:00Z" },
      { seq: 4, epoch: 4, kind: "leave", network_id: "x", created_at: "2026-10-25T13:00:00Z" },
    ],
    ...overrides,
  };
}

function msg(id: string, created_at: string, extra: Partial<Message> = {}): Message {
  return { id, from: { type: "agent", id: "brass" }, target: { kind: "room", room_id: "research" }, parts: [{ kind: "text", text: id }], created_at, ...extra };
}

// jsdom has no layout; give every element a viewport-sized box so the
// virtualized timeline renders its rows.
const sizes = ["offsetHeight", "offsetWidth"] as const;
const originals = sizes.map((key) => Object.getOwnPropertyDescriptor(HTMLElement.prototype, key));
beforeAll(() => {
  for (const key of sizes) Object.defineProperty(HTMLElement.prototype, key, { configurable: true, get: () => 800 });
});
afterAll(() => {
  sizes.forEach((key, i) => {
    const original = originals[i];
    if (original) Object.defineProperty(HTMLElement.prototype, key, original);
  });
});

let hub: RoomHub | null;
let hubStatus: number;
let messages: Message[];

beforeEach(() => {
  hub = hubFixture();
  hubStatus = 200;
  messages = [];
  vi.stubGlobal("fetch", vi.fn(async (path: string) => {
    if (path === "/v1/network") return Response.json({ id: "me", name: "Me", version: "1", capabilities: { event_stream: "sse" } });
    if (path === "/v1/rooms") return Response.json({ rooms: [{ id: "research", name: "research", members: ["brass", "lab:quill"] }] });
    if (path === "/v1/agents") return Response.json({ agents: [{ id: "brass", network_id: "me", connected: true }] });
    if (path === "/v1/dms") return Response.json({ dms: [] });
    if (path === "/v1/rooms/research/hub") return hubStatus === 200 ? Response.json(hub) : new Response("not found", { status: hubStatus });
    if (path.startsWith("/v1/rooms/research/messages")) return Response.json({ messages, page: { has_more: false } });
    return Response.json({});
  }));
});

function mount(children: ReactNode, path = "/console/room/research") {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
  const location = memoryLocation({ path });
  render(
    <Router base="/console" hook={location.hook}>
      <QueryClientProvider client={client}>
        <SelectionProvider>{children}</SelectionProvider>
      </QueryClientProvider>
    </Router>,
  );
  return client;
}

describe("room hub endpoint", () => {
  it("reads 404 as not a shared room", async () => {
    hubStatus = 404;
    await expect(api.roomHub("research")).resolves.toBeNull();
  });

  it("still surfaces other failures", async () => {
    hubStatus = 503;
    await expect(api.roomHub("research")).rejects.toThrow(/503/);
  });
});

describe("shared room header chip", () => {
  it("names the home network, with the global id and history policy as title", async () => {
    mount(<TimelineHeader selection={{ kind: "room", id: "research" }} loaded={0} hasOlder={false} hub={hubFixture()} />);
    const chip = await screen.findByText("shared · home: newsroom");
    expect(chip.getAttribute("title")).toBe("sha256:feed/research\nhistory: from_join");
  });

  it("says this network when it is the home", async () => {
    mount(<TimelineHeader selection={{ kind: "room", id: "research" }} loaded={0} hasOlder={false} hub={hubFixture({ is_home: true })} />);
    expect(await screen.findByText("shared · home: this network")).toBeTruthy();
  });

  it("adds nothing for an ordinary room", async () => {
    mount(<TimelineHeader selection={{ kind: "room", id: "research" }} loaded={0} hasOlder={false} hub={null} />);
    expect(screen.queryByText(/^shared/)).toBeNull();
  });
});

describe("grouped roster", () => {
  it("groups members by network with role and trust, keeping local members", async () => {
    mount(<MembersPanel />);
    const home = await screen.findByRole("region", { name: "network newsroom" });
    expect(within(home).getByText("owner")).toBeTruthy();
    expect(within(home).getByText("· home")).toBeTruthy();
    expect(within(home).getByText("✓").getAttribute("title")).toBe("Key pinned independently (abababab)");

    const lab = screen.getByRole("region", { name: "network lab" });
    expect(within(lab).getByText("writer")).toBeTruthy();
    expect(within(lab).getByText("introduced by newsroom")).toBeTruthy();
    expect(within(lab).queryByText("✓")).toBeNull();
    expect(within(lab).getByText("quill")).toBeTruthy();

    const self = screen.getByRole("region", { name: "network me" });
    expect(within(self).getByText("this network")).toBeTruthy();
    expect(within(self).getByText("brass")).toBeTruthy();
  });

  it("keeps the plain member list for an ordinary room", async () => {
    hubStatus = 404;
    mount(<MembersPanel />);
    const self = await screen.findByRole("region", { name: "network me" });
    expect(within(self).getByText("brass")).toBeTruthy();
    await waitFor(() => expect(screen.queryByRole("region", { name: "network newsroom" })).toBeNull());
    expect(screen.queryByText("this network")).toBeNull();
  });
});

describe("control events", () => {
  const loaded = [msg("a", "2026-10-25T10:00:00Z"), msg("b", "2026-10-25T11:00:00Z"), msg("c", "2026-10-25T12:00:00Z")];
  const describeRows = (rows: ReturnType<typeof buildTimelineRows>) =>
    rows.map((row) => (row.kind === "message" ? row.message.id : `#${row.event.seq}`));

  it("interleaves the log with messages by created_at", () => {
    expect(describeRows(buildTimelineRows(loaded, hubFixture(), false))).toEqual(["#1", "a", "#2", "b", "#3", "c", "#4"]);
  });

  it("holds back events older than the loaded window while older pages remain", () => {
    expect(describeRows(buildTimelineRows(loaded, hubFixture(), true))).toEqual(["a", "#2", "b", "#3", "c", "#4"]);
  });

  it("breaks a sender run around a system line", () => {
    const rows = buildTimelineRows(loaded.map((m, i) => ({ ...m, created_at: `2026-10-25T10:0${i}:00Z` })), hubFixture({
      events: [{ seq: 9, kind: "add", network_id: "lab", role: "writer", created_at: "2026-10-25T10:01:30Z" }],
    }), false);
    const continued = rows.flatMap((row) => (row.kind === "message" ? [row.continued] : []));
    expect(continued).toEqual([false, true, false]);
  });

  it("renders each kind as a centered system line in the timeline", async () => {
    messages = loaded;
    hub = hubFixture({
      events: [
        ...hubFixture().events!,
        { seq: 5, kind: "set_role", network_id: "lab", role: "reader", created_at: "2026-10-25T13:01:00Z" },
        { seq: 6, kind: "set_policy", history: "full", created_at: "2026-10-25T13:02:00Z" },
      ],
    });
    mount(<Timeline />);
    expect(await screen.findByText("shared room created by newsroom")).toBeTruthy();
    const lines = screen.getAllByRole("note").map((node) => node.textContent);
    expect(lines).toEqual([
      "shared room created by newsroom",
      "lab joined (writer)",
      "old was removed",
      "x left",
      "lab is now reader",
      "history policy set to full",
    ]);
    expect(screen.getByText("shared · home: newsroom")).toBeTruthy();
  });

  it("renders no system lines or chip for an ordinary room", async () => {
    messages = loaded;
    hubStatus = 404;
    mount(<Timeline />);
    expect(await screen.findByText("c")).toBeTruthy();
    expect(screen.queryAllByRole("note")).toHaveLength(0);
    expect(screen.queryByText(/^shared/)).toBeNull();
  });
});

class FakeEventSource {
  static current: FakeEventSource | null = null;
  onopen: (() => void) | null = null;
  onerror: (() => void) | null = null;
  private listeners = new Map<string, (event: MessageEvent) => void>();
  constructor() {
    FakeEventSource.current = this;
  }
  addEventListener(type: string, listener: (event: MessageEvent) => void) {
    this.listeners.set(type, listener);
  }
  removeEventListener(type: string) {
    this.listeners.delete(type);
  }
  close() {}
  emit(type: string, data: unknown) {
    this.listeners.get(type)?.(new MessageEvent(type, { data: JSON.stringify(data) }));
  }
}

describe("live updates", () => {
  it("brings in the signature badge after a stream message, without a reload", async () => {
    vi.stubGlobal("EventSource", FakeEventSource);
    hubStatus = 404;
    messages = [msg("a", "2026-10-25T10:00:00Z")];
    mount(<EventStreamProvider><Timeline /></EventStreamProvider>);
    expect(await screen.findByText("a")).toBeTruthy();
    await waitFor(() => expect(FakeEventSource.current).not.toBeNull());

    // The stream event itself carries no signature; the refetched page does.
    const live = msg("b", "2026-10-25T10:05:00Z", { from: { type: "agent", id: "lab:quill", network_id: "lab" } });
    messages = [...messages, { ...live, origin: { signature: "verified", fingerprint: FP } }];
    act(() => FakeEventSource.current!.emit("message.created", { type: "message.created", created_at: live.created_at, message: live }));
    expect((await screen.findByTitle(/^Signature verified/)).textContent).toBe("✓");
  });
});
