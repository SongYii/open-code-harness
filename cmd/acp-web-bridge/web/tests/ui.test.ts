import { describe, expect, it } from "vitest";
import { Ledger } from "../src/ledger";
import { TrajectoryView } from "../src/ui";

function buildSnapshot() {
  const ledger = new Ledger();
  ledger.beginTurn("turn-1", "write a file please", 0);
  ledger.apply(
    {
      sessionUpdate: "tool_call",
      toolCallId: "turn-1/call-1",
      title: "write_file",
      kind: "edit",
      status: "pending",
      rawInput: { path: "a.txt" },
    },
    1,
  );
  ledger.apply(
    {
      sessionUpdate: "tool_call_update",
      toolCallId: "turn-1/call-1",
      status: "completed",
      content: [{ type: "content", content: { type: "text", text: "wrote 3 bytes" } }],
    },
    5,
  );
  ledger.endTurn(6);
  return ledger.snapshot();
}

describe("TrajectoryView", () => {
  it("renders a turn separator and one row per record", () => {
    const root = document.createElement("div");
    const view = new TrajectoryView(root);
    view.render(buildSnapshot());

    const separators = view.getLedgerElement().querySelectorAll(".turn-separator");
    expect(separators).toHaveLength(1);
    expect(separators[0]?.textContent).toContain("turn-1");

    const rows = view.getLedgerElement().querySelectorAll(".record");
    expect(rows).toHaveLength(2); // the user message + the tool record
  });

  it("opens the inspector with the record's fields when a row is clicked", () => {
    const root = document.createElement("div");
    const view = new TrajectoryView(root);
    view.render(buildSnapshot());

    const toolRow = view.getLedgerElement().querySelector(".record-tool");
    expect(toolRow).not.toBeNull();
    (toolRow as HTMLElement).click();

    expect(view.getInspectorElement().hidden).toBe(false);
    const text = view.getInspectorElement().textContent ?? "";
    expect(text).toContain("write_file");
    expect(text).toContain("completed");
    expect(text).toContain("wrote 3 bytes");
    expect(text).toContain("local approximation");

    const selected = view.getSelected();
    expect(selected?.kind).toBe("tool");
  });
});
