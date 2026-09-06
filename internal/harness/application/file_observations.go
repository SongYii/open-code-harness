package application

import (
	"sync"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/tools"
)

type fileObservation struct {
	present bool
	version tools.FileVersion
}

type fileObservations struct {
	mu        sync.RWMutex
	bySession map[domain.SessionID]map[string]fileObservation
}

func newFileObservations() *fileObservations {
	return &fileObservations{bySession: make(map[domain.SessionID]map[string]fileObservation)}
}

func (observations *fileObservations) guardForWrite(sessionID domain.SessionID, target string) tools.MutationGuard {
	observations.mu.RLock()
	observation, ok := observations.bySession[sessionID][target]
	observations.mu.RUnlock()
	if ok && observation.present {
		return tools.MutationGuard{Kind: tools.GuardReplaceIfVersion, Version: observation.version}
	}
	return tools.MutationGuard{Kind: tools.GuardCreateIfAbsent}
}

func (observations *fileObservations) guardForEdit(sessionID domain.SessionID, target string) (tools.MutationGuard, error) {
	observations.mu.RLock()
	observation, ok := observations.bySession[sessionID][target]
	observations.mu.RUnlock()
	if !ok {
		return tools.MutationGuard{}, &tools.Error{Code: tools.CodeFilesystemNotObserved}
	}
	if !observation.present {
		return tools.MutationGuard{}, &tools.Error{Code: tools.CodeFilesystemNotFound}
	}
	return tools.MutationGuard{Kind: tools.GuardReplaceIfVersion, Version: observation.version}, nil
}

func (observations *fileObservations) recordPresent(sessionID domain.SessionID, target string, version tools.FileVersion) {
	observations.mu.Lock()
	defer observations.mu.Unlock()
	byTarget := observations.bySession[sessionID]
	if byTarget == nil {
		byTarget = make(map[string]fileObservation)
		observations.bySession[sessionID] = byTarget
	}
	byTarget[target] = fileObservation{present: true, version: version}
}

func (observations *fileObservations) recordAbsent(sessionID domain.SessionID, target string) {
	observations.mu.Lock()
	defer observations.mu.Unlock()
	byTarget := observations.bySession[sessionID]
	if byTarget == nil {
		byTarget = make(map[string]fileObservation)
		observations.bySession[sessionID] = byTarget
	}
	byTarget[target] = fileObservation{}
}

func (observations *fileObservations) forget(sessionID domain.SessionID) {
	observations.mu.Lock()
	delete(observations.bySession, sessionID)
	observations.mu.Unlock()
}
