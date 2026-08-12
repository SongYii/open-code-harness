# Task 1 Architecture Gate: Assistant Item Lifecycle

- Date: 2026-08-12
- Scope: Engine plan Task 1, with load-bearing implications for Tasks 2 and 4
- Verdict: **READY_WITH_AMENDMENTS**
- Implementation state at review: not started

## Evidence

| Project | Primary evidence | Relevant behavior | Decision for Open Code Harness |
| --- | --- | --- | --- |
| OpenAI Codex | [`codex-rs/app-server/README.md`](https://github.com/openai/codex/blob/main/codex-rs/app-server/README.md) | A Turn contains multiple typed Items; clients receive `item/started`, type-specific deltas, `item/completed`, then `turn/completed`; final Items are persisted context while realtime notifications are delivery signals. | Adopt generic Item identity/lifecycle, typed payloads, and explicit terminal ordering. Do not copy the public JSON-RPC model into the domain. |
| Maka | [`ARCHITECTURE.md`](https://github.com/maka-agent/maka-agent/blob/main/ARCHITECTURE.md) | Runtime Event Log is the fact authority; Session, context, UI, and recovery are projections driven by one Runtime authority. | Adopt immutable facts, replay validation, and atomic Item/Turn terminal facts. Defer Runtime Host/process design. |
| Pi | [`packages/agent/src/agent-loop.ts`](https://github.com/badlogic/pi-mono/blob/main/packages/agent/src/agent-loop.ts) | Assistant messages are accumulated through a small injectable loop while lifecycle/delta events are emitted transiently. | Adopt exact final assistant payload plus transient deltas. Do not treat the in-memory message list as durable authority. |
| Kimi Code | [`AGENTS.md`](https://github.com/MoonshotAI/kimi-code/blob/main/AGENTS.md) | Transcript operations have monotonic per-scope batch sequencing and catch-up semantics. | Adopt ordered batch facts and explicit scope ownership. Defer transcript replication and DI scope machinery. |
| Mini-Agent | [`MiniMax-AI/Mini-Agent`](https://github.com/MiniMax-AI/Mini-Agent) | Provides a runnable integrated model/tool/logging path. | Retain as end-to-end reference only; do not derive the domain Item model from its provider-oriented integration. |

The public `MiniMax-AI/minimax-code` repository remains issue-only and supplies no implementation evidence.

## Findings

### Important 1: A generic Item must not become a flattened field bag

The accepted design correctly anticipates future tool, reasoning, and image Items, but the original Task 1 plan placed `Text`, failure fields, and interruption fields directly on every Item. Adding future kinds would grow a sparse union with combinations that the type system cannot reject.

Amendment:

```go
type Item struct {
    ID           ItemID
    TurnID       TurnID
    Kind         ItemKind
    Status       ItemStatus
    Payload      ItemPayload
    StartedAt    time.Time
    EndedAt      time.Time
    Terminal     *ItemTerminal
}

type ItemPayload interface {
    ItemKind() ItemKind
    cloneItemPayload() ItemPayload
}

type AssistantMessagePayload struct {
    Text string
}

type ItemTerminal struct {
    Code    string
    Message string
}
```

`ItemPayload` is a closed domain sum: its unexported clone method prevents external implementations. For this milestone the only implementation is `AssistantMessagePayload`. `Terminal` is nil for running/completed Items and required for failed/interrupted Items. A completed assistant payload contains exact final text; running, failed, and interrupted assistant payload text is empty because partial deltas are not durable.

### Important 2: Apply must reject malformed pre-state

Transition guards alone assume `ActiveItemID`, `ItemOrder`, and `Items` are already mutually consistent. Direct callers and replay can otherwise transition from a corrupt state and produce a plausible result.

Before applying an Item event or a Turn terminal event, validate every affected Turn:

- `ItemOrder` contains no duplicate ID and references every map entry exactly once;
- each map key equals `Item.ID`, and every Item belongs to that Turn;
- every Item has a known kind/status and matching closed payload kind;
- at most one Item is running;
- `ActiveItemID` is empty iff no Item is running, otherwise it identifies that running Item;
- running Items have zero `EndedAt` and nil `Terminal`;
- completed Items have non-zero `EndedAt`, nil `Terminal`, and a valid assistant payload;
- failed/interrupted Items have non-zero `EndedAt`, a required stable terminal code, and a valid optional UTF-8 display message.

Malformed input returns `CodeInvalidEvent` without mutating input state.

### Important 3: Terminal failure/interruption data needs machine semantics

Provider prose and a free-form interruption reason are not sufficient for evaluation or recovery. New Item terminal events use a required stable `Code` and optional safe `Message`. The Engine/Application layer maps raw causes to the stable catalog; the domain validates but does not interpret provider errors.

Initial stable interruption codes are `caller_canceled` and `runtime_delivery_failed`. Existing `turn.interrupted.reason` remains schema-compatible and carries the same stable code. Existing `turn.failed` already has `code` and `message`. Raw provider payloads never enter Item or Turn facts.

### Important 4: One append batch has one occurrence time

`assistant.message.completed` followed by `turn.completed` describes one atomic transition. Calling the clock independently per record makes the Turn appear later for an implementation detail and complicates deterministic evidence.

The EventStore must call `Clock.Now()` once per append request. All records in the batch receive that normalized UTC value, distinct Event IDs, one Command ID, and contiguous sequence values. Item terminal fact precedes Turn terminal fact.

### Important 5: Schema version and compatibility must be explicit

`schemaVersion: 1` versions the recorded-event envelope and per-event strict payload encoding; it is not a frozen enumeration of every event type. Because these events are internal pre-v0, new recognized event types may be added under version 1 without changing existing encodings.

Task 1 must prove that the existing `session_lifecycle.jsonl` fixture still decodes, replays to the same state, and re-marshals each record byte-for-byte. Unknown event types and fields remain rejected.

## Adopt, reject, defer

Adopt now:

- generic Item identity, ownership, lifecycle, closed typed payload, and stable terminal metadata;
- exact final assistant text as a durable fact and deltas as runtime-only signals;
- deep immutable clone and corrupt-pre-state rejection;
- Item-before-Turn terminal ordering, one CAS batch, one command, one timestamp;
- backward-compatible schema-v1 catalog extension.

Reject now:

- one flattened Item struct with every future kind's fields;
- `any`, raw JSON, provider SDK objects, or external ItemPayload implementations in domain state;
- durable token deltas;
- automatic repair of malformed state during replay;
- independent timestamps for records in one append request.

Defer:

- ToolCall, ToolResult, Reasoning, Image, Approval, and ModelAttempt payload variants;
- public protocol Item schemas and compatibility guarantees;
- transcript pagination, compaction, pruning, and replication.

## Final Task 1 contract

Task 1 may begin after the accepted spec and plan carry these amendments:

1. Add `ItemID`, Item identity/lifecycle, a closed `ItemPayload`, `AssistantMessagePayload`, and `ItemTerminal`.
2. Nest Items under Turn with deep cloning and strict ownership/order/active consistency.
3. Add four assistant-message events. Completed carries exact `Text`; failed/interrupted carry required `Code` and optional safe `Message`.
4. Apply validates malformed pre-state and returns `CodeInvalidEvent` without mutation.
5. Codec adds strict new event payloads under envelope schema version 1 and preserves every old fixture byte/meaning.
6. Task 2 returns Item terminal before Turn terminal and uses the same stable interruption code.
7. Task 4 assigns one UTC occurrence time to every record in one atomic append batch.

No Task 1 production code should be written before these amendments are incorporated. With them incorporated, the architecture gate is **READY**.
