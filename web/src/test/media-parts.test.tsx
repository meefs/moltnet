import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { MessageParts } from "../components/Content/Timeline/MessageParts";
import type { MessagePart, Network } from "../lib/types";

let network: Network;

beforeEach(() => {
  network = { id: "n", name: "N", version: "1", capabilities: {}, console: { viewer: { kind: "anonymous" } } };
  vi.stubGlobal("fetch", vi.fn(async () => Response.json(network)));
});

function mount(parts: MessagePart[]) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const { container } = render(
    <QueryClientProvider client={client}>
      <MessageParts parts={parts} />
    </QueryClientProvider>,
  );
  return { client, container };
}

const image: MessagePart = { kind: "image", url: "https://cdn.example/chart.png", media_type: "image/png" };

describe("media parts", () => {
  it("loads nothing while the viewer is still unknown", () => {
    vi.stubGlobal("fetch", vi.fn(() => new Promise<Response>(() => {})));
    const { container } = mount([image, { kind: "audio", url: "https://cdn.example/a.mp3" }]);
    expect(container.querySelector("img")).toBeNull();
    expect(container.querySelector("audio")).toBeNull();
  });

  it("asks an anonymous viewer before loading an image", async () => {
    const { client, container } = mount([image]);
    await waitFor(() => expect(client.getQueryData(["network"])).toBeTruthy());
    expect(container.querySelector("img")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: /load image from cdn\.example/ }));
    const img = container.querySelector("img");
    expect(img?.getAttribute("src")).toBe("https://cdn.example/chart.png");
    expect(img?.getAttribute("referrerpolicy")).toBe("no-referrer");
  });

  it("loads images straight away for a signed-in viewer", async () => {
    network.console = { viewer: { kind: "token", id: "operator" } };
    const { container } = mount([image]);
    await waitFor(() => expect(container.querySelector("img")).toBeTruthy());
  });

  it("plays audio only after asking an anonymous viewer", async () => {
    const { client, container } = mount([{ kind: "audio", url: "https://cdn.example/brief.mp3" }]);
    await waitFor(() => expect(client.getQueryData(["network"])).toBeTruthy());
    expect(container.querySelector("audio")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: /load audio/ }));
    expect(container.querySelector("audio")?.getAttribute("src")).toBe("https://cdn.example/brief.mp3");
  });

  it("never loads or links a non-web url", async () => {
    network.console = { viewer: { kind: "token", id: "operator" } };
    const { container } = mount([
      { kind: "image", url: "javascript:alert(1)" },
      { kind: "url", url: "javascript:alert(1)" },
      { kind: "file", url: "data:text/html,hi", filename: "x.html" },
    ]);
    await waitFor(() => expect(screen.getAllByText(/link not allowed/).length).toBe(2));
    expect(container.querySelector("img")).toBeNull();
    expect(container.querySelector("a")).toBeNull();
    expect(screen.getByText("javascript:alert(1)")).toBeTruthy();
  });

  it("labels a file with its format", () => {
    mount([{ kind: "file", url: "https://files.example/report.pdf", media_type: "application/pdf" }]);
    expect(screen.getByText("PDF")).toBeTruthy();
    expect(screen.getByText("report.pdf")).toBeTruthy();
    expect(screen.getByRole("link", { name: "open" }).getAttribute("href")).toBe("https://files.example/report.pdf");
  });
});
