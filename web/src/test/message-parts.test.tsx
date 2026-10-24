import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { continuesFrom, TimelineMessage } from "../components/Content/Timeline/TimelineMessage";
import type { Message } from "../lib/types";

function message(text: string, overrides: Partial<Message> = {}): Message {
  return {
    from: { type: "agent", id: "brass", network_id: "local" },
    target: { kind: "room", room_id: "desk" },
    parts: [{ kind: "text", text }],
    created_at: "2026-10-24T10:00:00Z",
    ...overrides,
  };
}

describe("message parts", () => {
  it("renders a JSON object written as text as a data card", () => {
    render(<TimelineMessage message={message('{"kind":"research.answer.v1","status":"refused"}')} />);
    expect(screen.getByText("research.answer.v1")).toBeTruthy();
    expect(screen.getByText("status")).toBeTruthy();
    expect(screen.queryByText(/"status"/)).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "raw json" }));
    expect(screen.getByText(/"status": "refused"/)).toBeTruthy();
  });

  it("keeps prose that merely starts with a brace as text", () => {
    render(<TimelineMessage message={message("{not json} but words")} />);
    expect(screen.getByText("{not json} but words")).toBeTruthy();
  });

  it("marks only credential-bound local senders as verified", () => {
    const bound = message("hi", { from: { type: "agent", id: "brass", network_id: "local", credential_bound: true } });
    const { rerender } = render(<TimelineMessage message={bound} />);
    expect(screen.getByTitle("identity proven with a credential on this network")).toBeTruthy();
    rerender(<TimelineMessage message={{ ...bound, origin: { received_via: "peer" } }} />);
    expect(screen.queryByTitle("identity proven with a credential on this network")).toBeNull();
  });
});

describe("grouping", () => {
  it("continues the same sender within five minutes only", () => {
    const first = message("a");
    expect(continuesFrom(first, message("b", { created_at: "2026-10-24T10:04:00Z" }))).toBe(true);
    expect(continuesFrom(first, message("b", { created_at: "2026-10-24T10:06:00Z" }))).toBe(false);
    expect(continuesFrom(first, message("b", { from: { type: "agent", id: "spike" } }))).toBe(false);
    expect(continuesFrom(first, message("b", { origin: { received_via: "peer" } }))).toBe(false);
  });

  it("never lets an unverified or differently typed row hide under a verified header", () => {
    const bound = message("a", { from: { type: "agent", id: "brass", network_id: "local", credential_bound: true } });
    expect(continuesFrom(bound, message("b", { created_at: "2026-10-24T10:01:00Z" }))).toBe(false);
    expect(continuesFrom(message("a"), message("b", { from: { type: "human", id: "brass", network_id: "local" } }))).toBe(false);
    expect(continuesFrom(bound, { ...bound, created_at: "2026-10-24T10:01:00Z" })).toBe(true);
  });
});
