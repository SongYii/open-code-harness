import type { LedgerRecord, LedgerSnapshot, Turn } from "./ledger";

/** PermissionRequestView is the subset of ACP's session/request_permission
 * params (internal/client/acp/permission.go's permissionRequestParams,
 * read directly) this view needs to render the correlated tool call
 * inline while the composer is taken over. */
export interface PermissionRequestView {
  toolTitle: string;
  toolKind: string;
}

interface PendingPermission {
  resolve(optionId: string): void;
}

interface QueuedPermission {
  request: PermissionRequestView;
  resolve(optionId: string): void;
}

// TrajectoryView renders a LedgerSnapshot as a turn-grouped ledger with a
// per-record inspector opened on selection (design §6), plus a composer
// that a pending session/request_permission call takes over in place
// (design §6: composer-position takeover, not a modal). Virtualized/
// windowed rendering for very long sessions is out of scope for this
// slice (plan Task 5): every loaded record is mounted directly.
export class TrajectoryView {
  private readonly ledgerEl: HTMLElement;
  private readonly inspectorEl: HTMLElement;
  private readonly composerEl: HTMLElement;
  private readonly promptInput: HTMLInputElement;
  private readonly submitButton: HTMLButtonElement;
  private selected: LedgerRecord | null = null;
  private onSubmit: ((text: string) => void) | null = null;
  private pendingPermission: PendingPermission | null = null;
  private readonly queuedPermissions: QueuedPermission[] = [];

  constructor(root: HTMLElement) {
    this.ledgerEl = document.createElement("div");
    this.ledgerEl.className = "ledger";
    this.inspectorEl = document.createElement("div");
    this.inspectorEl.className = "inspector";
    this.inspectorEl.hidden = true;

    this.composerEl = document.createElement("div");
    this.composerEl.className = "composer";
    this.promptInput = document.createElement("input");
    this.promptInput.type = "text";
    this.submitButton = document.createElement("button");
    this.submitButton.textContent = "Send";
    this.submitButton.addEventListener("click", () => this.submitPrompt());
    this.promptInput.addEventListener("keydown", (event: KeyboardEvent) => {
      if (event.key === "Enter") this.submitPrompt();
    });
    this.restoreComposer();

    root.append(this.ledgerEl, this.composerEl, this.inspectorEl);
  }

  /** onPromptSubmit registers the callback the composer invokes with the
   * operator's typed text. Submission is a no-op while a permission
   * request is pending (isComposerDisabled). */
  onPromptSubmit(handler: (text: string) => void): void {
    this.onSubmit = handler;
  }

  isComposerDisabled(): boolean {
    return this.pendingPermission !== null;
  }

  private submitPrompt(): void {
    if (this.isComposerDisabled()) return;
    const text = this.promptInput.value.trim();
    if (!text) return;
    this.promptInput.value = "";
    this.onSubmit?.(text);
  }

  /** showPermissionRequest replaces the composer with request's detail and
   * allow-once/reject controls, resolving once the operator decides. A
   * second request arriving while one is already pending — not expected
   * under ACP's own one-prompt-in-flight rule, but defended here anyway —
   * is queued and rendered only after the first is answered, rather than
   * corrupting or silently dropping either. */
  showPermissionRequest(request: PermissionRequestView): Promise<string> {
    return new Promise((resolve) => {
      if (this.pendingPermission) {
        this.queuedPermissions.push({ request, resolve });
        return;
      }
      this.pendingPermission = { resolve };
      this.renderPermissionPrompt(request);
    });
  }

  private renderPermissionPrompt(request: PermissionRequestView): void {
    this.composerEl.replaceChildren();
    const detail = document.createElement("span");
    detail.className = "permission-detail";
    detail.textContent = `Approve ${request.toolTitle} (${request.toolKind})?`;
    const allowButton = document.createElement("button");
    allowButton.className = "permission-allow";
    allowButton.textContent = "Allow once";
    allowButton.addEventListener("click", () => this.resolvePermission("allow-once"));
    const rejectButton = document.createElement("button");
    rejectButton.className = "permission-reject";
    rejectButton.textContent = "Reject";
    rejectButton.addEventListener("click", () => this.resolvePermission("reject-once"));
    this.composerEl.append(detail, allowButton, rejectButton);
  }

  private resolvePermission(optionId: string): void {
    const pending = this.pendingPermission;
    if (!pending) return;
    this.pendingPermission = null;
    pending.resolve(optionId);

    const next = this.queuedPermissions.shift();
    if (next) {
      this.pendingPermission = { resolve: next.resolve };
      this.renderPermissionPrompt(next.request);
      return;
    }
    this.restoreComposer();
  }

  private restoreComposer(): void {
    this.composerEl.replaceChildren(this.promptInput, this.submitButton);
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

  getComposerElement(): HTMLElement {
    return this.composerEl;
  }
}
