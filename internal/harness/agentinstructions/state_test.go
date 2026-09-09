package agentinstructions

import (
	"reflect"
	"strings"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

const (
	digestRoot    = "sha256:53175bcc0524f37b47062fafdda28e3f8eb91d519ca0a184ca71bbebe72f969a"
	digestDeep    = "sha256:64896f89fd11190013b70103e603a1c5826e56b7fb7d2197ab279b0690043599"
	digestChanged = "sha256:7f8b1dfc466b6249f06cbe55c9174df2578e7754da793fded244ef5cba2a38f1"
	digestRootSet = "sha256:afc246a24b10eb221c7267a042182d9624bd970057c39c094095d320efac1e77"
)

func TestReconcileSetReplaceRemoveAndReappear(t *testing.T) {
	state := State{}
	batch, state, err := Reconcile(state, []Observation{{Path: "AGENTS.md", Scope: ".", Present: true, Content: []byte("root\n")}})
	if err != nil {
		t.Fatalf("initial Reconcile() error = %v", err)
	}
	wantSet := domain.InstructionChange{Action: domain.InstructionActionSet, Path: "AGENTS.md", Scope: ".", Digest: digestRoot, Content: "root\n"}
	if batch.Epoch != 1 || !reflect.DeepEqual(batch.Changes, []domain.InstructionChange{wantSet}) {
		t.Fatalf("initial batch = %#v", batch)
	}
	if state.Epoch != 1 || state.Sources["AGENTS.md"].Digest != digestRoot || EffectiveSetDigest(state) != digestRootSet {
		t.Fatalf("initial state = %#v, digest = %q", state, EffectiveSetDigest(state))
	}

	unchanged, same, err := Reconcile(state, []Observation{{Path: "AGENTS.md", Scope: ".", Present: true, Content: []byte("root\n")}})
	if err != nil || !unchanged.Empty() || !reflect.DeepEqual(same, state) {
		t.Fatalf("unchanged Reconcile() = (%#v, %#v, %v)", unchanged, same, err)
	}

	replaced, state, err := Reconcile(state, []Observation{{Path: "AGENTS.md", Scope: ".", Present: true, Content: []byte("changed\n")}})
	wantReplace := domain.InstructionChange{Action: domain.InstructionActionReplace, Path: "AGENTS.md", Scope: ".", PriorDigest: digestRoot, Digest: digestChanged, Content: "changed\n"}
	if err != nil || replaced.Epoch != 2 || !reflect.DeepEqual(replaced.Changes, []domain.InstructionChange{wantReplace}) {
		t.Fatalf("replace Reconcile() = (%#v, %#v, %v)", replaced, state, err)
	}

	removed, state, err := Reconcile(state, []Observation{{Path: "AGENTS.md", Scope: ".", Present: false}})
	wantRemove := domain.InstructionChange{Action: domain.InstructionActionRemove, Path: "AGENTS.md", Scope: ".", PriorDigest: digestChanged}
	if err != nil || removed.Epoch != 3 || !reflect.DeepEqual(removed.Changes, []domain.InstructionChange{wantRemove}) || len(state.Sources) != 0 {
		t.Fatalf("remove Reconcile() = (%#v, %#v, %v)", removed, state, err)
	}

	reappeared, state, err := Reconcile(state, []Observation{{Path: "AGENTS.md", Scope: ".", Present: true, Content: []byte("root\n")}})
	if err != nil || reappeared.Epoch != 4 || reappeared.Changes[0].Action != domain.InstructionActionSet || state.Sources["AGENTS.md"].Digest != digestRoot {
		t.Fatalf("reappear Reconcile() = (%#v, %#v, %v)", reappeared, state, err)
	}
}

func TestReconcileSortsShallowToDeepAndRetainsStateOnDiagnostic(t *testing.T) {
	state := State{}
	batch, state, err := Reconcile(state, []Observation{
		{Path: "src/pkg/AGENTS.md", Scope: "src/pkg", Present: true, Content: []byte("deep\n")},
		{Path: "AGENTS.md", Scope: ".", Present: true, Content: []byte("root\n")},
	})
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if got := []string{batch.Changes[0].Path, batch.Changes[1].Path}; !reflect.DeepEqual(got, []string{"AGENTS.md", "src/pkg/AGENTS.md"}) {
		t.Fatalf("change order = %#v", got)
	}

	diagnostic, next, err := Reconcile(state, []Observation{{Path: "AGENTS.md", Scope: ".", FailureClass: "temporarily_unreadable"}})
	if err != nil || len(diagnostic.Changes) != 0 || len(diagnostic.Diagnostics) != 1 || !reflect.DeepEqual(next.Sources, state.Sources) {
		t.Fatalf("diagnostic Reconcile() = (%#v, %#v, %v)", diagnostic, next, err)
	}
}

func TestReplayRequiresHistoricalTransitionContinuity(t *testing.T) {
	firstBatch, firstState, err := Reconcile(State{}, []Observation{{Path: "AGENTS.md", Scope: ".", Present: true, Content: []byte("root\n")}})
	if err != nil {
		t.Fatal(err)
	}
	secondBatch, want, err := Reconcile(firstState, []Observation{{Path: "AGENTS.md", Scope: ".", Present: true, Content: []byte("changed\n")}})
	if err != nil {
		t.Fatal(err)
	}
	records := []domain.RecordedEvent{
		{Sequence: 2, Event: recordedFromBatch(firstBatch, firstState)},
		{Sequence: 3, Event: recordedFromBatch(secondBatch, want)},
	}
	got, err := Replay(records)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("Replay() = (%#v, %v), want %#v", got, err, want)
	}

	broken := records
	bad := broken[1].Event.(domain.WorkspaceInstructionsRecorded)
	bad.Changes[0].PriorDigest = digestDeep
	broken[1].Event = bad
	if _, err := Replay(broken); err == nil || !strings.Contains(err.Error(), "prior digest") {
		t.Fatalf("Replay(broken) error = %v", err)
	}
}

func TestReconcileOwnsInputAndReturnedState(t *testing.T) {
	content := []byte("root\n")
	_, state, err := Reconcile(State{}, []Observation{{Path: "AGENTS.md", Scope: ".", Present: true, Content: content}})
	if err != nil {
		t.Fatal(err)
	}
	content[0] = 'X'
	clone := state.Clone()
	source := clone.Sources["AGENTS.md"]
	source.Content = "changed"
	clone.Sources["AGENTS.md"] = source
	if state.Sources["AGENTS.md"].Content != "root\n" {
		t.Fatalf("state aliases caller or clone: %#v", state)
	}
}

func TestReconcileRefusesSource257BeforeReadingItsContent(t *testing.T) {
	state := State{Discovered: make(map[string]string, MaxSources)}
	for index := 0; index < MaxSources; index++ {
		path := "p" + strings.Repeat("x", index) + "/AGENTS.md"
		state.Discovered[path] = strings.TrimSuffix(path, "/AGENTS.md")
	}
	before := state.Clone()
	batch, next, err := Reconcile(state, []Observation{{Path: "overflow/AGENTS.md", Scope: "overflow", Present: true, Content: make([]byte, MaxSourceBytes+1)}})
	if err == nil || !strings.Contains(err.Error(), "256") || !batch.Empty() || !reflect.DeepEqual(next, before) {
		t.Fatalf("Reconcile(source 257) = (%#v, %#v, %v)", batch, next, err)
	}
}

func TestReconcileRefusesBatchThatWouldCrossSourceLimit(t *testing.T) {
	state := State{Discovered: make(map[string]string, MaxSources-1)}
	for index := 0; index < MaxSources-1; index++ {
		path := "p" + strings.Repeat("x", index) + "/AGENTS.md"
		state.Discovered[path] = strings.TrimSuffix(path, "/AGENTS.md")
	}
	before := state.Clone()
	batch, next, err := Reconcile(state, []Observation{
		{Path: "new-a/AGENTS.md", Scope: "new-a", Present: false},
		{Path: "new-b/AGENTS.md", Scope: "new-b", Present: false},
	})
	if err == nil || !batch.Empty() || !reflect.DeepEqual(next, before) {
		t.Fatalf("Reconcile(crossing batch) = (%#v, %#v, %v)", batch, next, err)
	}
}

func recordedFromBatch(batch Batch, state State) domain.WorkspaceInstructionsRecorded {
	message, _ := RenderBatch(batch, state)
	return domain.WorkspaceInstructionsRecorded{
		FormatVersion:      domain.WorkspaceInstructionsFormatV1,
		PromptID:           PromptID,
		PromptDigest:       PromptDigest,
		Epoch:              batch.Epoch,
		Discovered:         batch.Discovered,
		Changes:            batch.Changes,
		Diagnostics:        batch.Diagnostics,
		RenderedMessage:    message,
		EffectiveSetDigest: EffectiveSetDigest(state),
	}
}
