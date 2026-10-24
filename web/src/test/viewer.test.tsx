import { act, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { Router } from "wouter";
import { memoryLocation } from "wouter/memory-location";
import type { ReactNode } from "react";
import type { Network, Room } from "../lib/types";
import { TopBar } from "../components/TopBar/TopBar";
import { Sidebar } from "../components/Sidebar/Sidebar";
import { PairingsList } from "../components/Sidebar/PairingsPanel";
import { PairingView } from "../components/Content/PairingView/PairingView";
import { StatusBar } from "../components/StatusBar/StatusBar";
import { SelectionProvider, useSelection } from "../providers/SelectionProvider";

vi.mock("../providers/EventStreamProvider", () => ({
  useEventStream: () => ({ status: "unsupported", events: [] }),
}));
vi.mock("../hooks/useMessages", () => ({ useMessages: () => ({ data: undefined }) }));

let network: Network;
let rooms: Room[];
let pairingStatus: number;
let agentsStatus: number;
let roomsResponse: Promise<Response> | undefined;

beforeEach(() => {
  network = { id: "mirror", name: "Mirror", version: "1", capabilities: { direct_messages: true, pairings: true } };
  rooms = [];
  pairingStatus = agentsStatus = 200;
  roomsResponse = undefined;
  window.localStorage.clear();
  vi.stubGlobal("fetch", vi.fn(async (path: string) => {
    if (path === "/v1/network") return Response.json(network);
    if (path === "/v1/rooms") return roomsResponse ?? Response.json({ rooms });
    if (path === "/v1/pairings") return Response.json({ pairings: [] }, { status: pairingStatus });
    if (path === "/v1/agents") return Response.json({ agents: [] }, { status: agentsStatus });
    return Response.json({});
  }));
});

function mount(children: ReactNode, path = "/console/room/explicit") {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity, gcTime: 0 } } });
  const location = memoryLocation({ path, record: true });
  render(<Router base="/console" hook={location.hook}>
    <QueryClientProvider client={client}>
      <SelectionProvider>{children}</SelectionProvider>
    </QueryClientProvider>
  </Router>);
  return { client, location };
}

function SelectionLabel() {
  const { selected } = useSelection();
  return <p data-testid="selection">{selected ? `${selected.kind}:${"id" in selected ? selected.id : ""}` : "none"}</p>;
}

describe("TopBar", () => {
  it.each([
    [true, true, "connect your agent"],
    [false, true, "follow this network"],
    [false, false, undefined],
  ] as const)("chooses one CTA for connect=%s public=%s", async (connect, publicRead, label) => {
    network.console = { can_connect_agent: connect };
    network.capabilities.public_read = publicRead;
    mount(<TopBar />);
    await screen.findByText("Mirror");
    for (const name of ["connect your agent", "follow this network"]) {
      const link = screen.queryByRole("link", { name });
      expect(!!link).toBe(name === label);
      if (link) expect(link.getAttribute("href")).toBe("/install.md");
    }
  });

  it.each([true, false, undefined])("gates the CTA with can_connect_agent=%s", async (allowed) => {
    network.console = { can_connect_agent: allowed };
    mount(<TopBar />);
    await screen.findByText("Mirror");
    await waitFor(() => expect(!!screen.queryByRole("link", { name: "connect your agent" })).toBe(allowed ?? true));
  });
  it.each(["anonymous", "token", "agent"] as const)("shows the badge only for anonymous (%s)", async (kind) => {
    network.console = { can_send_human: false, viewer: { kind, id: "reader" } };
    mount(<TopBar />);
    await screen.findByText("Mirror");
    await waitFor(() => expect(!!screen.queryByText("read-only")).toBe(kind === "anonymous"));
  });
  it("does not label an anonymous writer read-only", async () => {
    network.console = { can_send_human: true, viewer: { kind: "anonymous" } };
    mount(<TopBar />);
    await screen.findByText("Mirror");
    expect(screen.queryByText("read-only")).toBeNull();
  });
});

describe("Sidebar", () => {
  it("labels a followed room as a read-only mirror", async () => {
    rooms = [{ id: "news.general", name: "General", follow: { id: "news", remote_base_url: "https://origin.example", remote_room_id: "general" } }];
    mount(<Sidebar />);
    expect(await screen.findByText("following news / read-only")).toBeTruthy();
    expect(screen.getByText("mirror")).toBeTruthy();
  });

  it.each([
    [true, true, true, true], [true, true, false, false],
    [false, false, true, true], [true, true, undefined, undefined],
  ])("combines feature and caller gates (%s %s %s %s)", async (pairings, dms, readPairings, readDMs) => {
    network.capabilities = { pairings, direct_messages: dms };
    network.console = { can_read_pairings: readPairings, can_read_dms: readDMs };
    const { client } = mount(<Sidebar />);
    await waitFor(() => expect(client.getQueryData(["network"])).toEqual(network));
    await waitFor(() => {
      expect(!!screen.queryByRole("tab", { name: "PAIRINGS" })).toBe(pairings && (readPairings ?? true));
      expect(!!screen.queryByRole("tab", { name: "DMS" })).toBe(dms && (readDMs ?? true));
    });
    expect(screen.getByRole("tab", { name: "AGENTS" })).toBeTruthy();
  });
  it.each([401, 403])("hides AGENTS after a forbidden fetch (%s)", async (status) => {
    agentsStatus = status;
    mount(<Sidebar />);
    await waitFor(() => expect(screen.queryByRole("tab", { name: "AGENTS" })).toBeNull());
  });
});

describe("pairings failures", () => {
  it.each([401, 403, 503])("shows a request error instead of empty (%s)", async (status) => {
    pairingStatus = status;
    mount(<PairingsList />);
    expect((await screen.findByRole("alert")).textContent).toBe(status === 503 ? "pairings unavailable." : "pairings access forbidden.");
    expect(screen.queryByText("no pairings connected.")).toBeNull();
  });
  it("shows the true empty state", async () => {
    mount(<PairingsList />);
    expect(await screen.findByText("no pairings connected.")).toBeTruthy();
    expect(screen.queryByRole("alert")).toBeNull();
  });
  it("shows a pairing route request error instead of not found", async () => {
    pairingStatus = 403;
    mount(<PairingView />, "/console/pairing/remote");
    expect((await screen.findByRole("alert")).textContent).toBe("pairings access forbidden.");
    expect(screen.queryByText(/pairing not found/)).toBeNull();
  });
  it("shows not found after a successful empty response", async () => {
    mount(<PairingView />, "/console/pairing/remote");
    expect(await screen.findByText("pairing not found — remote.")).toBeTruthy();
  });
});

describe("landing", () => {
  it("opens the most recent room with history replace and escapes its id", async () => {
    rooms = [
      { id: "first", name: "First", last_message_at: "2026-01-01T00:00:00Z" },
      { id: "busy / room", name: "Busy", last_message_at: "2026-02-01T00:00:00Z" },
    ];
    const { location } = mount(<SelectionLabel />, "/console/");
    await waitFor(() => expect(screen.getByTestId("selection").textContent).toBe("room:busy / room"));
    expect(location.history).toEqual(["/console/room/busy%20%2F%20room"]);
  });
  it("uses list order when activity is absent", async () => {
    rooms = [{ id: "first", name: "First" }, { id: "second", name: "Second" }];
    mount(<SelectionLabel />, "/console/");
    await waitFor(() => expect(screen.getByTestId("selection").textContent).toBe("room:first"));
  });
  it.each(["/room/chosen", "/dm/chosen", "/agent/chosen", "/pairing/chosen", "/events", "/unknown"])("preserves explicit route %s", async (path) => {
    rooms = [{ id: "busy", name: "Busy" }];
    const { client, location } = mount(<SelectionLabel />, `/console${path}`);
    await waitFor(() => expect(client.getQueryData(["rooms"])).toEqual(rooms));
    expect(location.history).toEqual([`/console${path}`]);
  });
  it("preserves navigation while the room request is pending", async () => {
    let resolve!: (value: Response) => void;
    roomsResponse = new Promise<Response>((done) => { resolve = done; });
    const { location } = mount(<SelectionLabel />, "/console/");
    act(() => location.navigate("/console/room/chosen"));
    await act(async () => resolve(Response.json({ rooms: [{ id: "busy", name: "Busy" }] })));
    expect(screen.getByTestId("selection").textContent).toBe("room:chosen");
    expect(location.history).toEqual(["/console/", "/console/room/chosen"]);
  });
  it("stays at landing for an empty list", async () => {
    const { client, location } = mount(<SelectionLabel />, "/console/");
    await waitFor(() => expect(client.getQueryData(["rooms"])).toEqual([]));
    expect(location.history).toEqual(["/console/"]);
  });
});

describe("StatusBar", () => {
  it.each([
    ["anonymous", undefined, "public"], ["token", "observer", "observer"], ["agent", "reader", "reader"],
  ] as const)("labels viewer %s", async (kind, id, expected) => {
    network.console = { viewer: { kind, id } };
    mount(<StatusBar />);
    expect(await screen.findByText(expected)).toBeTruthy();
    expect(screen.getByText("network:").parentElement?.textContent).toBe("network:mirror");
    expect(screen.getByText("viewer:").parentElement?.textContent).toBe(`viewer:${expected}`);
    for (const label of ["session:", "user:", "mode:", "operator"]) expect(screen.queryByText(label)).toBeNull();
  });
});
