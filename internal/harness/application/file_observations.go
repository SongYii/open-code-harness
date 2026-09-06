package application

import (
	"sync"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/tools"
)

// fileObservation is what one session has seen about one target.
//
// The zero value is not "absent" — a missing map entry means unseen, and an
// entry with present=false means the session looked and found nothing there.
// The distinction is the whole reason this type exists: an edit against an
// unseen file needs a read first, while an edit against a file known to be
// absent needs a different answer entirely.
type fileObservation struct {
	present bool
	version tools.FileVersion
}

// fileObservations is the per-session table of what has been read.
//
// It is process-local and deliberately never persisted. A version is a fact
// about a file on this machine at this moment; writing one into a Domain event
// would make it look like durable history, and a session resumed on another
// host would then carry guards describing files it has never seen. Clearing at
// the lifecycle boundaries in session.go is what keeps the table honest
// instead.
type fileObservations struct {
	mu        sync.RWMutex
	bySession map[domain.SessionID]map[string]fileObservation
}

func newFileObservations() *fileObservations {
	return &fileObservations{bySession: make(map[domain.SessionID]map[string]fileObservation)}
}

// guardForWrite derives the promise a write may make.
//
// An unseen or observed-absent target becomes create-if-absent, which fails
// closed: if something is in fact there, the adapter refuses rather than
// overwriting a file this session never looked at. A target observed present
// becomes replace-if-version, so a write loses to anyone who changed the file
// in between.
func (observations *fileObservations) guardForWrite(session domain.SessionID, target string) tools.MutationGuard {
	observed, ok := observations.lookup(session, target)
	if !ok || !observed.present {
		return tools.MutationGuard{Kind: tools.GuardCreateIfAbsent}
	}
	return tools.MutationGuard{Kind: tools.GuardReplaceIfVersion, Version: observed.version}
}

// guardForEdit derives the promise an edit may make, and refuses when there is
// no honest one to make.
//
// An edit is always a change to text the caller claims to have seen, so unlike
// a write it has no fail-closed fallback: there is no such thing as editing a
// file you have not read. The two refusals are different codes because they
// call for different next steps — read the file, versus stop looking for text
// in a file that is not there.
func (observations *fileObservations) guardForEdit(session domain.SessionID, target string) (tools.MutationGuard, error) {
	observed, ok := observations.lookup(session, target)
	switch {
	case !ok:
		return tools.MutationGuard{}, &tools.Error{Code: tools.CodeFSNotObserved}
	case !observed.present:
		return tools.MutationGuard{}, &tools.Error{Code: tools.CodeFSEditNotFound}
	}
	return tools.MutationGuard{Kind: tools.GuardReplaceIfVersion, Version: observed.version}, nil
}

// seen reports whether this session has any observation of the target at all,
// present or absent.
//
// It exists to tell two refusals apart that the adapter cannot distinguish. A
// create-if-absent guard refused because something is there means "the file
// changed" when the session had observed the target absent, and means "you
// never looked" when it had not.
func (observations *fileObservations) seen(session domain.SessionID, target string) bool {
	_, ok := observations.lookup(session, target)
	return ok
}

func (observations *fileObservations) recordPresent(session domain.SessionID, target string, version tools.FileVersion) {
	observations.record(session, target, fileObservation{present: true, version: version})
}

func (observations *fileObservations) recordAbsent(session domain.SessionID, target string) {
	observations.record(session, target, fileObservation{})
}

// forget drops everything one session observed.
//
// It is called at the lifecycle boundaries where the session's view of the
// workspace stops being trustworthy — a resume, whose gap may be arbitrarily
// long, and a close or delete, after which nothing should be held at all. It
// is deliberately not called from an ordinary load: observations have to
// survive turns, or every turn would start unable to edit anything.
func (observations *fileObservations) forget(session domain.SessionID) {
	observations.mu.Lock()
	delete(observations.bySession, session)
	observations.mu.Unlock()
}

func (observations *fileObservations) record(session domain.SessionID, target string, observed fileObservation) {
	observations.mu.Lock()
	defer observations.mu.Unlock()
	targets, ok := observations.bySession[session]
	if !ok {
		targets = make(map[string]fileObservation)
		observations.bySession[session] = targets
	}
	targets[target] = observed
}

func (observations *fileObservations) lookup(session domain.SessionID, target string) (fileObservation, bool) {
	observations.mu.RLock()
	defer observations.mu.RUnlock()
	observed, ok := observations.bySession[session][target]
	return observed, ok
}
