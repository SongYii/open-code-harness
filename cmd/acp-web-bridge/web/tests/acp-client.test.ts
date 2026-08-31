import { describe, expect, it, vi } from "vitest";
import {
  AcpClient,
  AcpError,
  WebSocketTransport,
  type Handler,
  type JsonValue,
  type Transport,
} from "../src/acp-client";

// FakeWebSocket stands in for the real browser WebSocket global so
// WebSocketTransport's queue-until-open behavior can be tested without a
// real network connection: it starts CONNECTING (no listeners fired
// yet) and only dispatches "open" when the test calls simulateOpen.
class FakeWebSocket {
  static instances: FakeWebSocket[] = [];
  sent: string[] = [];
  private openHandlers: Array<() => void> = [];

  constructor(public readonly url: string) {
    FakeWebSocket.instances.push(this);
  }

  addEventListener(type: string, handler: () => void): void {
    if (type === "open") this.openHandlers.push(handler);
  }

  send(data: string): void {
    this.sent.push(data);
  }

  simulateOpen(): void {
    for (const h of this.openHandlers) h();
  }
}

class FakeTransport implements Transport {
  sent: string[] = [];
  private handlers: Array<(data: string) => void> = [];

  send(data: string): void {
    this.sent.push(data);
  }

  onMessage(handler: (data: string) => void): void {
    this.handlers.push(handler);
  }

  // deliver simulates the agent sending one frame to this client.
  deliver(msg: unknown): void {
    const data = JSON.stringify(msg);
    for (const h of this.handlers) h(data);
  }

  lastSent(): unknown {
    const raw = this.sent[this.sent.length - 1];
    return raw === undefined ? undefined : JSON.parse(raw);
  }
}

function noopHandler(): Handler {
  return {
    handleSessionUpdate: vi.fn(),
    handleRequestPermission: vi.fn(async () => ({})),
  };
}

describe("AcpClient request/response correlation", () => {
  it("resolves the matching pending request even when responses arrive out of order", async () => {
    const transport = new FakeTransport();
    const client = new AcpClient(transport, noopHandler());

    const first = client.newSession("/a");
    const second = client.newSession("/b");

    const firstSent = JSON.parse(transport.sent[0]!) as { id: number };
    const secondSent = JSON.parse(transport.sent[1]!) as { id: number };

    // Deliver the SECOND request's response first.
    transport.deliver({ jsonrpc: "2.0", id: secondSent.id, result: { sessionId: "session-b" } });
    transport.deliver({ jsonrpc: "2.0", id: firstSent.id, result: { sessionId: "session-a" } });

    await expect(first).resolves.toBe("session-a");
    await expect(second).resolves.toBe("session-b");
  });

  it("rejects a pending request with an AcpError on a JSON-RPC error response", async () => {
    const transport = new FakeTransport();
    const client = new AcpClient(transport, noopHandler());

    const pending = client.newSession("/a");
    const sent = JSON.parse(transport.sent[0]!) as { id: number };
    transport.deliver({ jsonrpc: "2.0", id: sent.id, error: { code: -32602, message: "invalid params" } });

    await expect(pending).rejects.toBeInstanceOf(AcpError);
    await expect(pending).rejects.toMatchObject({ code: -32602, message: "invalid params" });
  });

  it("ignores a notification with no pending request and does not throw", () => {
    const transport = new FakeTransport();
    const handler = noopHandler();
    new AcpClient(transport, handler);

    expect(() =>
      transport.deliver({ jsonrpc: "2.0", method: "session/update", params: { kind: "tool_call" } }),
    ).not.toThrow();
    expect(handler.handleSessionUpdate).toHaveBeenCalledWith({ kind: "tool_call" });
  });
});

describe("AcpClient session/request_permission handling", () => {
  it("invokes the handler and sends its return value back as the RPC result", async () => {
    const transport = new FakeTransport();
    const handler: Handler = {
      handleSessionUpdate: vi.fn(),
      handleRequestPermission: vi.fn(async (params: JsonValue) => {
        expect(params).toMatchObject({ toolCall: { title: "write_file" } });
        return { outcome: { outcome: "selected", optionId: "allow-once" } };
      }),
    };
    new AcpClient(transport, handler);

    transport.deliver({
      jsonrpc: "2.0",
      id: "perm-1",
      method: "session/request_permission",
      params: { toolCall: { title: "write_file", kind: "edit" }, options: [] },
    });

    // Let the async handler's promise settle.
    await new Promise((resolve) => setTimeout(resolve, 0));

    expect(transport.lastSent()).toEqual({
      jsonrpc: "2.0",
      id: "perm-1",
      result: { outcome: { outcome: "selected", optionId: "allow-once" } },
    });
  });

  it("sends a JSON-RPC error response if the handler rejects", async () => {
    const transport = new FakeTransport();
    const handler: Handler = {
      handleSessionUpdate: vi.fn(),
      handleRequestPermission: vi.fn(async () => {
        throw new Error("boom");
      }),
    };
    new AcpClient(transport, handler);

    transport.deliver({
      jsonrpc: "2.0",
      id: "perm-2",
      method: "session/request_permission",
      params: { toolCall: { title: "exec", kind: "execute" }, options: [] },
    });

    await new Promise((resolve) => setTimeout(resolve, 0));

    const sent = transport.lastSent() as { error?: { message: string } };
    expect(sent.error?.message).toBe("boom");
  });
});

describe("AcpClient protocol calls", () => {
  it("sends the exact wire shape for session/prompt and returns the stop reason", async () => {
    const transport = new FakeTransport();
    const client = new AcpClient(transport, noopHandler());

    const pending = client.prompt("session-1", "hello");
    expect(transport.lastSent()).toMatchObject({
      method: "session/prompt",
      params: { sessionId: "session-1", prompt: [{ type: "text", text: "hello" }] },
    });

    const sent = transport.lastSent() as { id: number };
    transport.deliver({ jsonrpc: "2.0", id: sent.id, result: { stopReason: "end_turn" } });
    await expect(pending).resolves.toBe("end_turn");
  });

  it("sends session/cancel as a notification with no id", () => {
    const transport = new FakeTransport();
    const client = new AcpClient(transport, noopHandler());

    client.cancel("session-1");

    const sent = transport.lastSent() as { id?: unknown; method: string };
    expect(sent.id).toBeUndefined();
    expect(sent.method).toBe("session/cancel");
  });
});

describe("WebSocketTransport", () => {
  it("queues send() calls made before the socket opens and flushes them in order once it does", () => {
    const originalWebSocket = globalThis.WebSocket;
    // @ts-expect-error -- FakeWebSocket intentionally implements only the
    // slice of the real WebSocket API this transport uses.
    globalThis.WebSocket = FakeWebSocket;
    try {
      const transport = new WebSocketTransport("ws://example.test/ws");
      const socket = FakeWebSocket.instances[FakeWebSocket.instances.length - 1]!;

      // AcpClient's constructor calls initialize() immediately, well
      // before the WebSocket handshake can complete — this is exactly
      // that race, reproduced directly against the transport.
      transport.send("first");
      transport.send("second");
      expect(socket.sent).toEqual([]); // nothing sent yet; the socket is still CONNECTING

      socket.simulateOpen();
      expect(socket.sent).toEqual(["first", "second"]);

      transport.send("third");
      expect(socket.sent).toEqual(["first", "second", "third"]); // sent immediately once open
    } finally {
      globalThis.WebSocket = originalWebSocket;
    }
  });
});
