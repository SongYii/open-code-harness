import { describe, expect, it } from "vitest";
import { Ledger, type ToolRecord } from "../src/ledger";

describe("Ledger turn grouping", () => {
  it("reduces a tool_call then tool_call_update sequence into one ToolRecord keyed by toolCallId", () => {
    const ledger = new Ledger();
    ledger.beginTurn("turn-1", "please write a file", 1000);

    ledger.apply(
      {
        sessionUpdate: "tool_call",
        toolCallId: "turn-1/call-1",
        title: "write_file",
        kind: "edit",
        status: "pending",
        rawInput: { path: "a.txt" },
      },
      1001,
    );
    ledger.apply(
      {
        sessionUpdate: "tool_call_update",
        toolCallId: "turn-1/call-1",
        status: "completed",
        content: [{ type: "content", content: { type: "text", text: "wrote 3 bytes" } }],
      },
      1005,
    );
    ledger.endTurn(1006);

    const snapshot = ledger.snapshot();
    expect(snapshot.turns).toHaveLength(1);
    const turn = snapshot.turns[0]!;
    expect(turn.turnId).toBe("turn-1");
    expect(turn.startedAtMs).toBe(1000);
    expect(turn.endedAtMs).toBe(1006);
    expect(turn.records).toHaveLength(2); // user message + one tool record

    const tool = turn.records[1] as ToolRecord;
    expect(tool.kind).toBe("tool");
    expect(tool.toolCallId).toBe("turn-1/call-1");
    expect(tool.status).toBe("completed");
    expect(tool.content).toBe("wrote 3 bytes");
    expect(tool.rawInput).toEqual({ path: "a.txt" });
    expect(tool.endedAtMs).toBe(1005);
  });

  it("does not bleed records from one turn into the next", () => {
    const ledger = new Ledger();

    ledger.beginTurn("turn-1", "first", 0);
    ledger.apply(
      { sessionUpdate: "tool_call", toolCallId: "turn-1/call-1", title: "read_file", kind: "read", status: "pending" },
      1,
    );
    ledger.apply({ sessionUpdate: "agent_message_chunk", content: { type: "text", text: "done with turn 1" } }, 2);
    ledger.endTurn(3);

    ledger.beginTurn("turn-2", "second", 10);
    ledger.apply(
      { sessionUpdate: "tool_call", toolCallId: "turn-2/call-1", title: "write_file", kind: "edit", status: "pending" },
      11,
    );
    ledger.endTurn(12);

    const snapshot = ledger.snapshot();
    expect(snapshot.turns).toHaveLength(2);
    expect(snapshot.turns[0]!.turnId).toBe("turn-1");
    expect(snapshot.turns[1]!.turnId).toBe("turn-2");

    const turn1ToolIds = snapshot.turns[0]!.records.filter((r) => r.kind === "tool").map((r) => (r as ToolRecord).toolCallId);
    const turn2ToolIds = snapshot.turns[1]!.records.filter((r) => r.kind === "tool").map((r) => (r as ToolRecord).toolCallId);
    expect(turn1ToolIds).toEqual(["turn-1/call-1"]);
    expect(turn2ToolIds).toEqual(["turn-2/call-1"]);
  });

  it("places a tool call whose toolCallId has no separator in the unassigned bucket instead of crashing", () => {
    const ledger = new Ledger();
    ledger.beginTurn("turn-1", "first", 0);

    expect(() =>
      ledger.apply(
        { sessionUpdate: "tool_call", toolCallId: "malformed-no-slash", title: "exec", kind: "execute", status: "pending" },
        1,
      ),
    ).not.toThrow();

    const snapshot = ledger.snapshot();
    expect(snapshot.turns[0]!.records.filter((r) => r.kind === "tool")).toHaveLength(0);
    expect(snapshot.unassigned).toHaveLength(1);
    expect((snapshot.unassigned[0] as ToolRecord).toolCallId).toBe("malformed-no-slash");
  });

  it("marks every derived timing field as a local approximation", () => {
    const ledger = new Ledger();
    ledger.beginTurn("turn-1", "hi", 0);
    ledger.apply(
      { sessionUpdate: "tool_call", toolCallId: "turn-1/call-1", title: "read_file", kind: "read", status: "pending" },
      1,
    );

    const snapshot = ledger.snapshot();
    expect(snapshot.turns[0]!.approximateTiming).toBe(true);
    const tool = snapshot.turns[0]!.records[1] as ToolRecord;
    expect(tool.approximateTiming).toBe(true);
  });
});
