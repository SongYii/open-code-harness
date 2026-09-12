package application

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/SongYii/open-code-harness/internal/harness/agentinstructions"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/tools"
)

type workspaceInstructionRegistry struct {
	mu       sync.Mutex
	sessions map[domain.SessionID]*workspaceInstructionSession
}

type workspaceInstructionSession struct {
	mu          sync.Mutex
	initialized bool
	state       agentinstructions.State
	probes      map[string]instructionProbe
}

type instructionProbe struct {
	Version            tools.FileVersion
	Digest             string
	ActiveFailureClass string
}

func newWorkspaceInstructionRegistry() *workspaceInstructionRegistry {
	return &workspaceInstructionRegistry{sessions: make(map[domain.SessionID]*workspaceInstructionSession)}
}

func (registry *workspaceInstructionRegistry) session(id domain.SessionID) *workspaceInstructionSession {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	entry := registry.sessions[id]
	if entry == nil {
		entry = &workspaceInstructionSession{probes: make(map[string]instructionProbe)}
		registry.sessions[id] = entry
	}
	return entry
}

func (registry *workspaceInstructionRegistry) forget(id domain.SessionID) {
	if registry == nil {
		return
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	delete(registry.sessions, id)
}

// reconcileWorkspaceInstructions treats touched paths as directories whose
// lexical ancestor chain was reached by a successful structured filesystem
// operation. Callers translate file targets to their parent directory first.
func (service *Service) reconcileWorkspaceInstructions(ctx context.Context, session domain.Session, touched []string) (result domain.Session, returnErr error) {
	if service == nil || service.files == nil || service.instructions == nil {
		return session, nil
	}
	entry := service.instructions.session(session.ID)
	entry.mu.Lock()
	defer entry.mu.Unlock()

	if !entry.initialized {
		records, err := ReadWholeStreamPinned(ctx, service.store, session.ID, 256)
		if err != nil {
			return domain.Session{}, err
		}
		if len(records) == 0 || records[len(records)-1].Sequence != session.Version {
			return domain.Session{}, applicationError(CategoryConflict, "session_version_changed", true, nil)
		}
		replayed, err := agentinstructions.Replay(records)
		if err != nil {
			return domain.Session{}, storeContractViolation(err)
		}
		entry.state = replayed
		entry.probes = make(map[string]instructionProbe)
		entry.initialized = true
	}

	candidates, err := instructionCandidates(session.WorkspaceRoot, entry.state, touched)
	if err != nil {
		return domain.Session{}, applicationError(CategoryValidation, "invalid_workspace_instruction_target", false, err)
	}
	stagedProbes := cloneInstructionProbes(entry.probes)
	observations := make([]agentinstructions.Observation, 0, len(candidates))
	for _, candidate := range candidates {
		observation, observe, staged := service.observeInstruction(ctx, session.WorkspaceRoot, candidate, entry.state, entry.probes[candidate])
		if observe {
			observations = append(observations, observation)
		}
		if staged != nil {
			stagedProbes[candidate] = *staged
		}
	}

	batch, nextInstructions, err := agentinstructions.Reconcile(entry.state, observations)
	if err != nil {
		return domain.Session{}, applicationError(CategoryValidation, "workspace_instruction_invalid", false, err)
	}
	for path, probe := range stagedProbes {
		if source, ok := nextInstructions.Sources[path]; ok {
			probe.Digest = source.Digest
		} else {
			probe.Digest = ""
		}
		stagedProbes[path] = probe
	}
	if batch.Empty() {
		entry.probes = stagedProbes
		return session, nil
	}
	rendered, err := agentinstructions.RenderBatch(batch, nextInstructions)
	if err != nil {
		return domain.Session{}, applicationError(CategoryValidation, "workspace_instruction_render_failed", false, err)
	}
	payload := domain.WorkspaceInstructionsRecorded{
		FormatVersion:      domain.WorkspaceInstructionsFormatV1,
		PromptID:           agentinstructions.PromptID,
		PromptDigest:       agentinstructions.PromptDigest,
		Epoch:              batch.Epoch,
		Discovered:         batch.Discovered,
		Changes:            batch.Changes,
		Diagnostics:        batch.Diagnostics,
		RenderedMessage:    rendered,
		EffectiveSetDigest: agentinstructions.EffectiveSetDigest(nextInstructions),
	}
	decided, err := domain.Decide(session, domain.RecordWorkspaceInstructions{SessionID: session.ID, WorkspaceInstructionsRecorded: payload})
	if err != nil {
		return domain.Session{}, applicationError(CategoryInternal, "domain_rejected", false, err)
	}
	commandID, err := service.ids.NewCommandID()
	if mapped := generatedIDError(ctx, err); mapped != nil {
		return domain.Session{}, mapped
	}
	if _, err := domain.ParseCommandID(string(commandID)); err != nil {
		return domain.Session{}, applicationError(CategoryInternal, "id_generator_contract_violation", false, err)
	}
	intent, err := BuildAppendIntent(service.clock, service.ids, service.authority.CurrentAuthority(), session.ID, session.Version, commandID, nil, decided)
	if err != nil {
		return domain.Session{}, err
	}
	traceCtx, trace := startAppendTrace(ctx, service.telemetry, intent)
	defer func() { trace.end(returnErr) }()
	nextSession, _, err := CommitAppendIntent(traceCtx, service.store, session, intent)
	if isAppendOutcomeUnknown(err) {
		resolveCtx, cancel := context.WithTimeout(context.WithoutCancel(traceCtx), service.config.AppendResolutionTimeout)
		defer cancel()
		receipt, resolveErr := ResolveAppendIntent(resolveCtx, service.store, intent, service.appendResolutionConfig())
		if resolveErr != nil {
			return domain.Session{}, resolveErr
		}
		nextSession, _, err = ApplyCommittedIntent(session, intent, receipt)
	}
	if err != nil {
		return domain.Session{}, err
	}
	entry.state = nextInstructions
	entry.probes = stagedProbes
	return nextSession, nil
}

func (service *Service) observeInstruction(ctx context.Context, workspaceRoot, relative string, state agentinstructions.State, cached instructionProbe) (agentinstructions.Observation, bool, *instructionProbe) {
	scope := filepath.ToSlash(filepath.Dir(relative))
	abs, err := service.files.Resolve(ctx, workspaceRoot, filepath.FromSlash(relative))
	if err != nil {
		return failedInstructionObservation(relative, scope, classifyInstructionFailure(err), cached, state)
	}
	probe, err := service.files.Read(ctx, abs, 0)
	if errors.Is(err, fs.ErrNotExist) {
		staged := instructionProbe{}
		return agentinstructions.Observation{Path: relative, Scope: scope, Present: false}, true, &staged
	}
	if err != nil {
		return failedInstructionObservation(relative, scope, classifyInstructionFailure(err), cached, state)
	}
	if probe.Version == "" {
		return failedInstructionObservation(relative, scope, "invalid_version", cached, state)
	}
	if cached.Version == probe.Version && cached.ActiveFailureClass == "" {
		return agentinstructions.Observation{}, false, nil
	}
	read, err := service.files.Read(ctx, abs, agentinstructions.MaxSourceBytes)
	if errors.Is(err, fs.ErrNotExist) {
		staged := instructionProbe{}
		return agentinstructions.Observation{Path: relative, Scope: scope, Present: false}, true, &staged
	}
	if err != nil {
		return failedInstructionObservation(relative, scope, classifyInstructionFailure(err), cached, state)
	}
	if read.Truncated {
		return failedInstructionObservation(relative, scope, "too_large", cached, state)
	}
	if read.Version == "" {
		return failedInstructionObservation(relative, scope, "invalid_version", cached, state)
	}
	staged := instructionProbe{Version: read.Version}
	return agentinstructions.Observation{Path: relative, Scope: scope, Present: true, Content: read.Data}, true, &staged
}

func failedInstructionObservation(relative, scope, class string, cached instructionProbe, state agentinstructions.State) (agentinstructions.Observation, bool, *instructionProbe) {
	if cached.ActiveFailureClass == class {
		if _, discovered := state.Discovered[relative]; discovered {
			return agentinstructions.Observation{}, false, nil
		}
	}
	staged := cached
	staged.ActiveFailureClass = class
	return agentinstructions.Observation{Path: relative, Scope: scope, FailureClass: class}, true, &staged
}

func instructionCandidates(workspaceRoot string, state agentinstructions.State, touched []string) ([]string, error) {
	root, err := CanonicalWorkspaceRoot(workspaceRoot)
	if err != nil {
		return nil, err
	}
	set := map[string]struct{}{"AGENTS.md": {}}
	for path := range state.Discovered {
		set[path] = struct{}{}
	}
	for _, directory := range touched {
		if !filepath.IsAbs(directory) {
			directory = filepath.Join(root, directory)
		}
		relative, err := filepath.Rel(root, filepath.Clean(directory))
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return nil, errors.New("instruction discovery target leaves workspace")
		}
		for relative != "." {
			set[filepath.ToSlash(filepath.Join(relative, "AGENTS.md"))] = struct{}{}
			relative = filepath.Dir(relative)
		}
	}
	paths := make([]string, 0, len(set))
	for path := range set {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths, nil
}

func cloneInstructionProbes(probes map[string]instructionProbe) map[string]instructionProbe {
	clone := make(map[string]instructionProbe, len(probes))
	for path, probe := range probes {
		clone[path] = probe
	}
	return clone
}

func classifyInstructionFailure(err error) string {
	if errors.Is(err, fs.ErrPermission) {
		return "permission_denied"
	}
	var toolErr *tools.Error
	if errors.As(err, &toolErr) && toolErr != nil {
		return string(toolErr.Code)
	}
	return "read_failed"
}
