// ledger.ts reduces raw ACP v1 session/update notifications (as delivered
// by AcpClient's Handler.handleSessionUpdate) into a turn-grouped view
// model, per the accepted design's §1.5/§6: no new wire field is needed
// because toolCallId already encodes turnID ("turnID/callID", acp-v1.md),
// and this project's own single-flight prompt rule (acp-v1.md: "Concurrent
// prompts on one session are -32600") means at most one turn is ever open
// live at a time.

export type ToolCallStatus = "pending" | "in_progress" | "completed" | "failed";

export interface ToolRecord {
  kind: "tool";
  toolCallId: string;
  turnId: string;
  title: string;
  toolKind: string;
  status: ToolCallStatus;
  rawInput?: unknown;
  content?: string;
  startedAtMs: number;
  endedAtMs?: number;
  /** True only for startedAtMs/endedAtMs above: a local, receipt-time
   * approximation this client derived itself, never a provider-reported
   * value ACP does not send (design §5). Always true here — kept as an
   * explicit field, not a comment, so ui.ts cannot render this timing
   * without carrying the label along with it. */
  approximateTiming: true;
}

export interface MessageRecord {
  kind: "user" | "assistant";
  turnId: string;
  text: string;
}

export type LedgerRecord = ToolRecord | MessageRecord;

export interface Turn {
  turnId: string;
  records: LedgerRecord[];
  startedAtMs: number;
  endedAtMs?: number;
  approximateTiming: true;
}

export interface LedgerSnapshot {
  turns: Turn[];
  unassigned: LedgerRecord[];
}

export interface MessageChunkUpdate {
  sessionUpdate: "agent_message_chunk" | "user_message_chunk";
  content: { type: "text"; text: string };
}

export interface ToolCallUpdate {
  sessionUpdate: "tool_call" | "tool_call_update";
  toolCallId: string;
  title?: string;
  kind?: string;
  status?: ToolCallStatus;
  content?: Array<{ type: string; content: { type: string; text: string } }>;
  rawInput?: unknown;
}

export type SessionUpdate = MessageChunkUpdate | ToolCallUpdate;

function isToolCallUpdate(update: SessionUpdate): update is ToolCallUpdate {
  return update.sessionUpdate === "tool_call" || update.sessionUpdate === "tool_call_update";
}

// splitToolCallId parses "turnID/callID" by the LAST "/", since callID
// itself is not guaranteed free of "/" — mirroring how a Go
// implementation would need to split this same identity. Returns null if
// there is no "/" at all, which this module treats as unparseable, not as
// "turnID is the whole string."
function splitToolCallId(toolCallId: string): { turnId: string; callId: string } | null {
  const idx = toolCallId.lastIndexOf("/");
  if (idx < 0) return null;
  return { turnId: toolCallId.slice(0, idx), callId: toolCallId.slice(idx + 1) };
}

function extractText(content?: Array<{ type: string; content: { type: string; text: string } }>): string | undefined {
  if (!content || content.length === 0) return undefined;
  return content.map((c) => c.content.text).join("");
}

export class Ledger {
  private readonly turnsById = new Map<string, Turn>();
  private readonly order: string[] = [];
  private readonly unassignedRecords: LedgerRecord[] = [];
  private currentTurnId: string | null = null;

  private turnFor(turnId: string, atMs: number): Turn {
    let turn = this.turnsById.get(turnId);
    if (!turn) {
      turn = { turnId, records: [], startedAtMs: atMs, approximateTiming: true };
      this.turnsById.set(turnId, turn);
      this.order.push(turnId);
    }
    return turn;
  }

  /** beginTurn opens a new live turn, keyed by a turnId this client
   * assigns itself (the wire carries no turn-start signal in live mode —
   * acp-v1.md: "the in-flight turn does not emit user_message_chunk").
   * Called by the UI layer at the moment it calls AcpClient.prompt. */
  beginTurn(turnId: string, userText: string, atMs: number): void {
    this.currentTurnId = turnId;
    const turn = this.turnFor(turnId, atMs);
    turn.records.push({ kind: "user", turnId, text: userText });
  }

  /** endTurn closes the currently open live turn, called when the
   * session/prompt call this turn started from resolves. */
  endTurn(atMs: number): void {
    if (this.currentTurnId === null) return;
    const turn = this.turnsById.get(this.currentTurnId);
    if (turn) turn.endedAtMs = atMs;
    this.currentTurnId = null;
  }

  /** apply reduces one session/update notification's `update` payload.
   * Tool-shaped updates are attributed by parsing toolCallId, never by
   * guessing from the currently open turn — this is what lets the same
   * reducer serve a future session/load replay path unchanged. A plain
   * text chunk carries no identifier of its own on the wire, so it is
   * attributed to whichever turn is currently open. */
  apply(update: SessionUpdate, atMs: number): void {
    if (isToolCallUpdate(update)) {
      this.applyToolCallUpdate(update, atMs);
      return;
    }
    this.applyMessageChunk(update, atMs);
  }

  private applyToolCallUpdate(update: ToolCallUpdate, atMs: number): void {
    const parsed = splitToolCallId(update.toolCallId);
    if (!parsed) {
      this.unassignedRecords.push({
        kind: "tool",
        toolCallId: update.toolCallId,
        turnId: "",
        title: update.title ?? "",
        toolKind: update.kind ?? "other",
        status: update.status ?? "pending",
        rawInput: update.rawInput,
        content: extractText(update.content),
        startedAtMs: atMs,
        approximateTiming: true,
      });
      return;
    }
    const turn = this.turnFor(parsed.turnId, atMs);
    let record = turn.records.find(
      (r): r is ToolRecord => r.kind === "tool" && r.toolCallId === update.toolCallId,
    );
    if (!record) {
      record = {
        kind: "tool",
        toolCallId: update.toolCallId,
        turnId: parsed.turnId,
        title: update.title ?? "",
        toolKind: update.kind ?? "other",
        status: update.status ?? "pending",
        rawInput: update.rawInput,
        startedAtMs: atMs,
        approximateTiming: true,
      };
      turn.records.push(record);
    }
    if (update.title !== undefined) record.title = update.title;
    if (update.kind !== undefined) record.toolKind = update.kind;
    if (update.status !== undefined) record.status = update.status;
    if (update.rawInput !== undefined) record.rawInput = update.rawInput;
    const text = extractText(update.content);
    if (text !== undefined) record.content = text;
    if (update.status === "completed" || update.status === "failed") {
      record.endedAtMs = atMs;
    }
  }

  private applyMessageChunk(update: MessageChunkUpdate, atMs: number): void {
    const turnId = this.currentTurnId;
    if (turnId === null) {
      this.unassignedRecords.push({
        kind: update.sessionUpdate === "user_message_chunk" ? "user" : "assistant",
        turnId: "",
        text: update.content.text,
      });
      return;
    }
    const turn = this.turnFor(turnId, atMs);
    const kind = update.sessionUpdate === "user_message_chunk" ? "user" : "assistant";
    const last = turn.records[turn.records.length - 1];
    if (last?.kind === kind) {
      last.text += update.content.text;
      return;
    }
    turn.records.push({ kind, turnId, text: update.content.text });
  }

  snapshot(): LedgerSnapshot {
    return {
      turns: this.order.map((id) => this.turnsById.get(id)!),
      unassigned: [...this.unassignedRecords],
    };
  }
}
