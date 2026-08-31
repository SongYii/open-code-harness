// AcpClient is a genuinely independent ACP v1 client implementation. It
// does not import or wrap internal/client/acp (which cannot run in a
// browser) — it implements the same wire contract fresh, matching
// acp-v1.md's own method/param/result shapes exactly, per the accepted
// design's explicit requirement (design doc §6). The bridge it connects
// through (cmd/acp-web-bridge) never parses any of this: everything below
// is real ACP semantics living where the design says they must.

export type JsonValue = unknown;

interface RawMessage {
  jsonrpc?: string;
  id?: string | number;
  method?: string;
  params?: JsonValue;
  result?: JsonValue;
  error?: { code: number; message: string; data?: JsonValue };
}

/** Transport is what AcpClient sends framed JSON-RPC text over: a real
 * WebSocket in production (WebSocketTransport), a fake in tests. Neither
 * AcpClient nor a test hardcodes the WebSocket API into the dispatch
 * logic below. */
export interface Transport {
  send(data: string): void;
  onMessage(handler: (data: string) => void): void;
}

/** WebSocketTransport adapts a browser WebSocket to Transport. It is the
 * only place this file touches the WebSocket API directly. */
export class WebSocketTransport implements Transport {
  private readonly socket: WebSocket;

  constructor(url: string) {
    this.socket = new WebSocket(url);
  }

  send(data: string): void {
    this.socket.send(data);
  }

  onMessage(handler: (data: string) => void): void {
    this.socket.addEventListener("message", (event: MessageEvent) => {
      if (typeof event.data === "string") handler(event.data);
    });
  }
}

/** AcpError wraps a JSON-RPC error response's code and message. */
export class AcpError extends Error {
  readonly code: number;
  constructor(code: number, message: string) {
    super(message);
    this.code = code;
    this.name = "AcpError";
  }
}

/** Handler receives what an agent sends this client unprompted: a stream
 * of session/update notifications, and the one call an agent makes back
 * into the client, session/request_permission — mirroring
 * internal/client/acp.Handler's own split exactly, so a later task (the
 * ledger and the permission UI) can plug in without touching this file. */
export interface Handler {
  handleSessionUpdate(params: JsonValue): void;
  handleRequestPermission(params: JsonValue): Promise<JsonValue>;
}

/** Permission option ids this project's own agent uses for the common
 * two-option allow/reject shape (acp-v1.md;
 * internal/harness/adapters/acp/protocol.go's optionAllowOnce/
 * optionRejectOnce, read directly — not guessed from the abstract ACP
 * spec). */
export const PERMISSION_ALLOW_ONCE = "allow-once";
export const PERMISSION_REJECT_ONCE = "reject-once";

interface InitializeResult {
  agentName: string;
  agentVersion: string;
  loadSession: boolean;
}

interface PendingRequest {
  resolve(result: JsonValue): void;
  reject(err: Error): void;
}

export class AcpClient {
  private readonly transport: Transport;
  private readonly handler: Handler;
  private nextId = 1;
  private readonly pending = new Map<string | number, PendingRequest>();

  constructor(transport: Transport, handler: Handler) {
    this.transport = transport;
    this.handler = handler;
    this.transport.onMessage((data) => this.dispatch(data));
  }

  // dispatch classifies an inbound frame exactly the way
  // internal/client/acp/wire.go's message.isResponse/isRequest/
  // isNotification do: an id with no method is a response to one of our
  // own outgoing calls; an id with a method is an inbound call from the
  // agent; a method with no id is a notification.
  private dispatch(raw: string): void {
    let msg: RawMessage;
    try {
      msg = JSON.parse(raw) as RawMessage;
    } catch {
      return; // malformed frame; nothing this layer can usefully do
    }
    const hasId = msg.id !== undefined && msg.id !== null;
    const hasMethod = typeof msg.method === "string" && msg.method !== "";
    if (hasId && !hasMethod) {
      this.dispatchResponse(msg);
    } else if (hasId && hasMethod) {
      this.dispatchInboundRequest(msg);
    } else if (hasMethod) {
      this.dispatchNotification(msg);
    }
  }

  private dispatchResponse(msg: RawMessage): void {
    if (msg.id === undefined) return;
    const pending = this.pending.get(msg.id);
    if (!pending) return; // no matching outstanding request; ignore
    this.pending.delete(msg.id);
    if (msg.error) {
      pending.reject(new AcpError(msg.error.code, msg.error.message));
    } else {
      pending.resolve(msg.result);
    }
  }

  private dispatchNotification(msg: RawMessage): void {
    if (msg.method === "session/update") {
      this.handler.handleSessionUpdate(msg.params);
    }
    // Any other notification is silently ignored: this project's own
    // agent sends no other notification a client needs to act on
    // (acp-v1.md's "Never projected on ACP" list).
  }

  private dispatchInboundRequest(msg: RawMessage): void {
    if (msg.method !== "session/request_permission") {
      this.transport.send(
        JSON.stringify({
          jsonrpc: "2.0",
          id: msg.id,
          error: { code: -32601, message: "method not found" },
        }),
      );
      return;
    }
    this.handler
      .handleRequestPermission(msg.params)
      .then((result) => {
        this.transport.send(JSON.stringify({ jsonrpc: "2.0", id: msg.id, result }));
      })
      .catch((err: unknown) => {
        this.transport.send(
          JSON.stringify({
            jsonrpc: "2.0",
            id: msg.id,
            error: {
              code: -32603,
              message: err instanceof Error ? err.message : String(err),
            },
          }),
        );
      });
  }

  private call(method: string, params?: JsonValue): Promise<JsonValue> {
    const id = this.nextId++;
    return new Promise((resolve, reject) => {
      this.pending.set(id, { resolve, reject });
      this.transport.send(JSON.stringify({ jsonrpc: "2.0", id, method, params }));
    });
  }

  private notify(method: string, params?: JsonValue): void {
    this.transport.send(JSON.stringify({ jsonrpc: "2.0", method, params }));
  }

  async initialize(): Promise<InitializeResult> {
    const result = (await this.call("initialize", {
      protocolVersion: 1,
      clientCapabilities: {},
    })) as {
      agentCapabilities: { loadSession: boolean };
      agentInfo: { name: string; version: string };
    };
    return {
      agentName: result.agentInfo.name,
      agentVersion: result.agentInfo.version,
      loadSession: result.agentCapabilities.loadSession,
    };
  }

  async newSession(cwd: string): Promise<string> {
    const result = (await this.call("session/new", { cwd })) as { sessionId: string };
    return result.sessionId;
  }

  async loadSession(sessionId: string, cwd: string): Promise<void> {
    await this.call("session/load", { sessionId, cwd });
  }

  async prompt(sessionId: string, text: string): Promise<string> {
    const result = (await this.call("session/prompt", {
      sessionId,
      prompt: [{ type: "text", text }],
    })) as { stopReason: string };
    return result.stopReason;
  }

  // session/cancel is sent as a fire-and-forget notification, matching
  // this project's own agent-side cancellation semantics (acp-v1.md): the
  // in-flight prompt() call observes the resulting "cancelled" stop
  // reason on its own pending response, not a separate signal here.
  cancel(sessionId: string): void {
    this.notify("session/cancel", { sessionId });
  }
}
