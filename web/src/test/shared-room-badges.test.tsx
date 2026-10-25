import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { continuesFrom, TimelineMessage } from "../components/Content/Timeline/TimelineMessage";
import { shortFingerprint } from "../lib/format";
import type { Message, MessageOrigin } from "../lib/types";

const FP = `sha256:0123abcd${"e".repeat(56)}`;

function signed(origin: MessageOrigin | undefined, overrides: Partial<Message> = {}): Message {
  return {
    id: "m1",
    from: { type: "agent", id: "lab:writer", network_id: "lab" },
    origin,
    target: { kind: "room", room_id: "research" },
    parts: [{ kind: "text", text: "hello" }],
    created_at: "2026-10-25T12:00:00Z",
    ...overrides,
  };
}

describe("message signature badge", () => {
  it("shows a check mark for verified, with the short fingerprint in the title", () => {
    render(<TimelineMessage message={signed({ signature: "verified", fingerprint: FP })} />);
    const badge = screen.getByTitle("Signature verified: key pinned independently (0123abcd)");
    expect(badge.textContent).toBe("✓");
  });

  it("shows introduced-by in amber and never a check mark", () => {
    const { container } = render(
      <TimelineMessage message={signed({ signature: "introduced", fingerprint: FP, introduced_by: "newsroom" })} />,
    );
    const badge = screen.getByText("introduced by newsroom");
    expect(badge.getAttribute("data-trust")).toBe("amber");
    expect(badge.getAttribute("title")).toMatch(/vouched for only by the room home newsroom; identity not independently verified/);
    expect(container.textContent).not.toContain("✓");
  });

  it("shows a muted unconfirmed chip", () => {
    render(<TimelineMessage message={signed({ signature: "unconfirmed", fingerprint: FP })} />);
    const badge = screen.getByText("unconfirmed");
    expect(badge.getAttribute("data-trust")).toBe("muted");
    expect(badge.getAttribute("title")).toMatch(/^Signature valid against a key first seen on this pairing; not yet confirmed/);
  });

  it("shows a red invalid-signature chip", () => {
    const { container } = render(<TimelineMessage message={signed({ signature: "invalid" })} />);
    expect(screen.getByText("invalid signature").getAttribute("data-trust")).toBe("coral");
    expect(container.textContent).not.toContain("✓");
  });

  it.each([
    ["unsigned", { signature: "unsigned" } as MessageOrigin],
    ["absent signature", { received_via: undefined } as MessageOrigin],
    ["absent origin", undefined],
  ])("renders no badge for %s", (_label, origin) => {
    const { container } = render(<TimelineMessage message={signed(origin)} />);
    expect(container.querySelector("[data-trust]")).toBeNull();
    expect(container.textContent).not.toMatch(/✓|introduced|unconfirmed|invalid/);
  });

  it("starts a new header when the signature verdict changes within a sender run", () => {
    const first = signed({ signature: "verified", fingerprint: FP });
    const second = signed({ signature: "invalid", fingerprint: FP }, { id: "m2", created_at: "2026-10-25T12:01:00Z" });
    const third = signed({ signature: "invalid", fingerprint: FP }, { id: "m3", created_at: "2026-10-25T12:02:00Z" });
    expect(continuesFrom(first, second)).toBe(false);
    expect(continuesFrom(second, third)).toBe(true);
  });
});

describe("received time", () => {
  it("notes a receipt more than a few seconds after the signed creation time", () => {
    render(<TimelineMessage message={signed({ signature: "verified" }, { received_at: "2026-10-25T12:03:00Z" })} />);
    expect(screen.getByText(/^received /)).toBeTruthy();
    const time = document.querySelector("time");
    expect(time?.getAttribute("title")).toContain("· received ");
  });

  it("stays silent for a near-instant receipt or none at all", () => {
    const { container, rerender } = render(
      <TimelineMessage message={signed({ signature: "verified" }, { received_at: "2026-10-25T12:00:02Z" })} />,
    );
    expect(container.textContent).not.toContain("received");
    rerender(<TimelineMessage message={signed({ signature: "verified" })} />);
    expect(container.textContent).not.toContain("received");
  });
});

describe("shortFingerprint", () => {
  it("keeps the first 8 hex chars after sha256:", () => {
    expect(shortFingerprint(FP)).toBe("0123abcd");
    expect(shortFingerprint(undefined)).toBe("");
  });
});
