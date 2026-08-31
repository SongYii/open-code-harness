import type { LedgerRecord, LedgerSnapshot, Turn } from "./ledger";

// TrajectoryView renders a LedgerSnapshot as a turn-grouped ledger with a
// per-record inspector opened on selection (design §6). Virtualized/
// windowed rendering for very long sessions is out of scope for this
// slice (plan Task 5): every loaded record is mounted directly.
export class TrajectoryView {
  private readonly ledgerEl: HTMLElement;
  private readonly inspectorEl: HTMLElement;
  private selected: LedgerRecord | null = null;

  constructor(root: HTMLElement) {
    this.ledgerEl = document.createElement("div");
    this.ledgerEl.className = "ledger";
    this.inspectorEl = document.createElement("div");
    this.inspectorEl.className = "inspector";
    this.inspectorEl.hidden = true;
    root.append(this.ledgerEl, this.inspectorEl);
  }

  render(snapshot: LedgerSnapshot): void {
    this.ledgerEl.replaceChildren();
    for (const turn of snapshot.turns) {
      this.ledgerEl.append(this.renderTurnSeparator(turn));
      for (const record of turn.records) {
        this.ledgerEl.append(this.renderRecordRow(record));
      }
    }
    if (snapshot.unassigned.length > 0) {
      const header = document.createElement("div");
      header.className = "unassigned-header";
      header.textContent = "Unassigned";
      this.ledgerEl.append(header);
      for (const record of snapshot.unassigned) {
        this.ledgerEl.append(this.renderRecordRow(record));
      }
    }
  }

  private renderTurnSeparator(turn: Turn): HTMLElement {
    const el = document.createElement("div");
    el.className = "turn-separator";
    el.dataset["turnId"] = turn.turnId;
    el.textContent = `Turn ${turn.turnId}`;
    return el;
  }

  private renderRecordRow(record: LedgerRecord): HTMLElement {
    const row = document.createElement("div");
    row.className = `record record-${record.kind}`;
    row.textContent = this.rowLabel(record);
    row.addEventListener("click", () => this.select(record));
    return row;
  }

  private rowLabel(record: LedgerRecord): string {
    if (record.kind === "tool") return `[${record.status}] ${record.title || record.toolKind}`;
    return record.kind === "user" ? `You: ${record.text}` : `Assistant: ${record.text}`;
  }

  private select(record: LedgerRecord): void {
    this.selected = record;
    this.renderInspector(record);
  }

  private renderInspector(record: LedgerRecord): void {
    this.inspectorEl.hidden = false;
    this.inspectorEl.replaceChildren();
    if (record.kind === "tool") {
      this.inspectorEl.append(
        this.field("Title", record.title),
        this.field("Kind", record.toolKind),
        this.field("Status", record.status),
        this.field(
          "Raw input",
          record.rawInput !== undefined ? JSON.stringify(record.rawInput) : "",
        ),
        this.field("Content", record.content ?? ""),
        // The label itself names this a local approximation, not a
        // provider-reported value (design §5) — this is not left to a
        // tooltip or a separate legend the operator might miss.
        this.field(
          "Timing (local approximation, not provider-reported)",
          this.formatTiming(record.startedAtMs, record.endedAtMs),
        ),
      );
    } else {
      this.inspectorEl.append(this.field("Text", record.text));
    }
  }

  private formatTiming(startedAtMs: number, endedAtMs?: number): string {
    if (endedAtMs === undefined) return "in progress";
    return `${endedAtMs - startedAtMs} ms`;
  }

  private field(label: string, value: string): HTMLElement {
    const el = document.createElement("div");
    el.className = "field";
    const labelEl = document.createElement("span");
    labelEl.className = "field-label";
    labelEl.textContent = label;
    const valueEl = document.createElement("span");
    valueEl.className = "field-value";
    valueEl.textContent = value;
    el.append(labelEl, valueEl);
    return el;
  }

  getSelected(): LedgerRecord | null {
    return this.selected;
  }

  getInspectorElement(): HTMLElement {
    return this.inspectorEl;
  }

  getLedgerElement(): HTMLElement {
    return this.ledgerEl;
  }
}
