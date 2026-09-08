package agentinstructions

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	pathpkg "path"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

const (
	MaxSourceBytes  = 1 << 20
	MaxContextBytes = 64 << 10
	MaxSources      = 256
)

type Observation struct {
	Path         string
	Scope        string
	Present      bool
	Content      []byte
	FailureClass string
}

type Source struct {
	Path    string `json:"path"`
	Scope   string `json:"scope"`
	Digest  string `json:"digest"`
	Content string `json:"content"`
}

type State struct {
	Epoch      uint64
	Discovered map[string]string
	Sources    map[string]Source
}

type Batch struct {
	Epoch       uint64
	Discovered  []domain.InstructionScope
	Changes     []domain.InstructionChange
	Diagnostics []domain.InstructionDiagnostic
}

type Snapshot struct {
	Epoch           uint64
	ThroughSequence uint64
	Sources         []Source
	RenderedMessage string
	Digest          string
}

func (state State) Clone() State {
	clone := State{Epoch: state.Epoch}
	if state.Discovered != nil {
		clone.Discovered = make(map[string]string, len(state.Discovered))
		for key, value := range state.Discovered {
			clone.Discovered[key] = value
		}
	}
	if state.Sources != nil {
		clone.Sources = make(map[string]Source, len(state.Sources))
		for key, value := range state.Sources {
			clone.Sources[key] = value
		}
	}
	return clone
}

func (batch Batch) Empty() bool {
	return len(batch.Discovered) == 0 && len(batch.Changes) == 0 && len(batch.Diagnostics) == 0
}

func Reconcile(state State, observations []Observation) (Batch, State, error) {
	original := state.Clone()
	next := state.Clone()
	if next.Discovered == nil {
		next.Discovered = make(map[string]string)
	}
	if next.Sources == nil {
		next.Sources = make(map[string]Source)
	}
	discoveredCount := len(next.Discovered)

	ordered := append([]Observation(nil), observations...)
	sort.Slice(ordered, func(i, j int) bool {
		return lessScopedPath(ordered[i].Scope, ordered[i].Path, ordered[j].Scope, ordered[j].Path)
	})
	seen := make(map[string]struct{}, len(ordered))
	for _, observation := range ordered {
		if err := validatePathScope(observation.Path, observation.Scope); err != nil {
			return Batch{}, original, err
		}
		if _, duplicate := seen[observation.Path]; duplicate {
			return Batch{}, original, fmt.Errorf("duplicate instruction observation %q", observation.Path)
		}
		seen[observation.Path] = struct{}{}
		if _, known := next.Discovered[observation.Path]; !known {
			discoveredCount++
			if discoveredCount > MaxSources {
				return Batch{}, original, fmt.Errorf("at most %d instruction paths may be discovered", MaxSources)
			}
		}
	}

	batch := Batch{}
	for _, observation := range ordered {
		if _, known := next.Discovered[observation.Path]; !known {
			next.Discovered[observation.Path] = observation.Scope
			batch.Discovered = append(batch.Discovered, domain.InstructionScope{Path: observation.Path, Scope: observation.Scope})
		}
		if observation.FailureClass != "" {
			if !utf8.ValidString(observation.FailureClass) || strings.TrimSpace(observation.FailureClass) == "" {
				return Batch{}, original, errors.New("instruction failure class is invalid")
			}
			batch.Diagnostics = append(batch.Diagnostics, domain.InstructionDiagnostic{Path: observation.Path, Class: observation.FailureClass})
			continue
		}
		prior, existed := next.Sources[observation.Path]
		if !observation.Present {
			if existed {
				delete(next.Sources, observation.Path)
				batch.Changes = append(batch.Changes, domain.InstructionChange{Action: domain.InstructionActionRemove, Path: observation.Path, Scope: observation.Scope, PriorDigest: prior.Digest})
			}
			continue
		}
		if len(observation.Content) > MaxSourceBytes {
			return Batch{}, original, fmt.Errorf("instruction source %q exceeds %d bytes", observation.Path, MaxSourceBytes)
		}
		if !utf8.Valid(observation.Content) {
			return Batch{}, original, fmt.Errorf("instruction source %q is not valid UTF-8", observation.Path)
		}
		digest := sha256Digest(observation.Content)
		if existed && prior.Digest == digest {
			continue
		}
		source := Source{Path: observation.Path, Scope: observation.Scope, Digest: digest, Content: string(observation.Content)}
		next.Sources[observation.Path] = source
		change := domain.InstructionChange{Action: domain.InstructionActionSet, Path: source.Path, Scope: source.Scope, Digest: source.Digest, Content: source.Content}
		if existed {
			change.Action = domain.InstructionActionReplace
			change.PriorDigest = prior.Digest
		}
		batch.Changes = append(batch.Changes, change)
	}

	if batch.Empty() {
		return Batch{}, original, nil
	}
	next.Epoch++
	batch.Epoch = next.Epoch
	return batch, next, nil
}

func EffectiveSetDigest(state State) string {
	sources := orderedSources(state)
	encoded, _ := json.Marshal(sources)
	return sha256Digest(encoded)
}

func Replay(records []domain.RecordedEvent) (State, error) {
	state := State{Discovered: make(map[string]string), Sources: make(map[string]Source)}
	for _, record := range records {
		event, ok := record.Event.(domain.WorkspaceInstructionsRecorded)
		if !ok {
			continue
		}
		if event.Epoch != state.Epoch+1 {
			return State{}, fmt.Errorf("instruction epoch %d does not follow %d", event.Epoch, state.Epoch)
		}
		for _, discovered := range event.Discovered {
			if err := validatePathScope(discovered.Path, discovered.Scope); err != nil {
				return State{}, err
			}
			if _, exists := state.Discovered[discovered.Path]; exists {
				return State{}, fmt.Errorf("instruction path %q was discovered twice", discovered.Path)
			}
			state.Discovered[discovered.Path] = discovered.Scope
		}
		for _, change := range event.Changes {
			if err := applyHistoricalChange(&state, change); err != nil {
				return State{}, err
			}
		}
		state.Epoch = event.Epoch
		if got := EffectiveSetDigest(state); got != event.EffectiveSetDigest {
			return State{}, fmt.Errorf("effective instruction digest %q does not match event %q", got, event.EffectiveSetDigest)
		}
	}
	return state, nil
}

func applyHistoricalChange(state *State, change domain.InstructionChange) error {
	if err := validatePathScope(change.Path, change.Scope); err != nil {
		return err
	}
	prior, exists := state.Sources[change.Path]
	switch change.Action {
	case domain.InstructionActionSet:
		if exists {
			return fmt.Errorf("set instruction %q already exists", change.Path)
		}
	case domain.InstructionActionReplace:
		if !exists || prior.Digest != change.PriorDigest {
			return fmt.Errorf("replace instruction %q prior digest does not match", change.Path)
		}
	case domain.InstructionActionRemove:
		if !exists || prior.Digest != change.PriorDigest {
			return fmt.Errorf("remove instruction %q prior digest does not match", change.Path)
		}
		delete(state.Sources, change.Path)
		return nil
	default:
		return fmt.Errorf("instruction %q has invalid action %q", change.Path, change.Action)
	}
	if sha256Digest([]byte(change.Content)) != change.Digest {
		return fmt.Errorf("instruction %q content digest does not match", change.Path)
	}
	state.Sources[change.Path] = Source{Path: change.Path, Scope: change.Scope, Digest: change.Digest, Content: change.Content}
	return nil
}

func orderedSources(state State) []Source {
	sources := make([]Source, 0, len(state.Sources))
	for _, source := range state.Sources {
		sources = append(sources, source)
	}
	sort.Slice(sources, func(i, j int) bool {
		return lessScopedPath(sources[i].Scope, sources[i].Path, sources[j].Scope, sources[j].Path)
	})
	return sources
}

func lessScopedPath(leftScope, leftPath, rightScope, rightPath string) bool {
	leftDepth, rightDepth := scopeDepth(leftScope), scopeDepth(rightScope)
	if leftDepth != rightDepth {
		return leftDepth < rightDepth
	}
	return leftPath < rightPath
}

func scopeDepth(scope string) int {
	if scope == "." {
		return 0
	}
	return strings.Count(scope, "/") + 1
}

func validatePathScope(instructionPath, scope string) error {
	if !utf8.ValidString(instructionPath) || instructionPath == "" || strings.Contains(instructionPath, "\\") || pathpkg.IsAbs(instructionPath) || pathpkg.Clean(instructionPath) != instructionPath || pathpkg.Base(instructionPath) != "AGENTS.md" {
		return fmt.Errorf("invalid instruction path %q", instructionPath)
	}
	if scope != pathpkg.Dir(instructionPath) {
		return fmt.Errorf("instruction scope %q does not match path %q", scope, instructionPath)
	}
	return nil
}

func sha256Digest(content []byte) string {
	sum := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(sum[:])
}
