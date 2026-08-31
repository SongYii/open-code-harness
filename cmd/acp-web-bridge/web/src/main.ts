// main.ts is the application entry point: it wires AcpClient (the
// independent ACP v1 protocol layer), Ledger (the turn-grouped reducer),
// and TrajectoryView (rendering) together against this same origin's
// /config and /ws endpoints. It carries no ACP semantics of its own
// beyond this wiring — those live in acp-client.ts and ledger.ts, per the
// accepted design. This file is exercised by Task 8's real end-to-end
// interoperability proof, not a dedicated unit test, the same way
// cmd/acp-client's own main() (as opposed to its tested run()) has no
// direct unit test either — only integration-level coverage, since a
// meaningful unit test here would require refactoring this wiring behind
// an injectable boundary this small an entry point does not warrant.
import { AcpClient, WebSocketTransport } from "./acp-client";
import type { SessionUpdate } from "./ledger";
import { Ledger } from "./ledger";
import { TrajectoryView, type PermissionRequestView } from "./ui";

interface Config {
  cwd: string;
  resume?: string;
}

interface SessionUpdateNotification {
  update: SessionUpdate;
}

interface RequestPermissionParams {
  toolCall: { title: string; kind: string };
}

function currentToken(): string {
  return new URLSearchParams(window.location.search).get("token") ?? "";
}

async function fetchConfig(token: string): Promise<Config> {
  const response = await fetch(`/config?token=${encodeURIComponent(token)}`);
  if (!response.ok) {
    throw new Error(`GET /config: ${response.status}`);
  }
  return (await response.json()) as Config;
}

function websocketURL(token: string): string {
  const url = new URL("/ws", window.location.href);
  url.protocol = url.protocol === "https:" ? "wss:" : "ws:";
  url.searchParams.set("token", token);
  return url.toString();
}

async function main(): Promise<void> {
  const root = document.getElementById("app");
  if (!root) throw new Error("acp-web-bridge: missing #app root element");

  const token = currentToken();
  const config = await fetchConfig(token);

  const ledger = new Ledger();
  const view = new TrajectoryView(root);
  const status = document.createElement("div");
  status.id = "status";
  root.append(status);
  let turnCounter = 0;

  const transport = new WebSocketTransport(websocketURL(token));
  const client = new AcpClient(transport, {
    handleSessionUpdate(params) {
      const { update } = params as SessionUpdateNotification;
      ledger.apply(update, Date.now());
      view.render(ledger.snapshot());
    },
    async handleRequestPermission(params) {
      const { toolCall } = params as RequestPermissionParams;
      const requestView: PermissionRequestView = { toolTitle: toolCall.title, toolKind: toolCall.kind };
      const optionId = await view.showPermissionRequest(requestView);
      return { outcome: { outcome: "selected", optionId } };
    },
  });

  await client.initialize();

  const requestedSessionId = new URLSearchParams(window.location.search).get("session");
  const resumeId = config.resume || requestedSessionId;
  const sessionId = resumeId
    ? await (async () => {
        await client.loadSession(resumeId, config.cwd);
        return resumeId;
      })()
    : await client.newSession(config.cwd);

  const url = new URL(window.location.href);
  url.searchParams.set("session", sessionId);
  window.history.replaceState(null, "", url.toString());

  view.onPromptSubmit((text) => {
    const turnId = `local-turn-${turnCounter++}`;
    ledger.beginTurn(turnId, text, Date.now());
    view.render(ledger.snapshot());
    status.textContent = "";
    client
      .prompt(sessionId, text)
      .then((stopReason) => {
        status.textContent = `[${stopReason}]`;
      })
      .catch((err: unknown) => {
        status.textContent = `[error: ${err instanceof Error ? err.message : String(err)}]`;
      })
      .finally(() => {
        ledger.endTurn(Date.now());
        view.render(ledger.snapshot());
      });
  });
}

main().catch((err: unknown) => {
  console.error("acp-web-bridge:", err);
  document.body.textContent = `acp-web-bridge: ${err instanceof Error ? err.message : String(err)}`;
});
