package tools

// This file defines the vocabulary guarded file mutation is expressed in.
// It holds values and validation only: no I/O, no filesystem, no clock.
//
// The whole point of the mechanism is that a destructive change carries a
// promise about the state it expects to find. The model never sees or supplies
// any of it — Application derives a guard from what the session actually
// observed, after Policy and the Approver have run — so these types are an
// internal contract between Application and the filesystem adapter, not part
// of any model-facing schema.

// FileVersion is an opaque token identifying one observed state of one file.
//
// Callers compare versions for equality and never parse them. Keeping it
// opaque is what allows the adapter to change how a version is computed —
// size and modification time today, a content digest tomorrow — without any
// consumer having encoded an assumption about its shape.
type FileVersion string

// MaxEditFileBytes bounds the file an edit may be applied to.
//
// A literal edit reads the whole file into memory, searches it, and writes a
// replacement, so the bound is a memory bound rather than a policy about what
// files are worth editing. Larger files are refused with CodeTooLarge rather
// than silently truncated, because a partial edit of a source file is worse
// than a refused one.
const MaxEditFileBytes = 1 << 20

// GuardKind names the promise a mutation makes about the state it expects.
type GuardKind string

const (
	// GuardCreateIfAbsent succeeds only when nothing exists at the target.
	// It carries no version, because there is no prior state to name.
	GuardCreateIfAbsent GuardKind = "create_if_absent"

	// GuardReplaceIfVersion succeeds only when the target still carries the
	// named version. It is what makes "I read this, then wrote it" safe
	// against something else having written in between.
	GuardReplaceIfVersion GuardKind = "replace_if_version"
)

// MutationGuard is the precondition a destructive change is allowed under.
//
// There is deliberately no "unconditional" kind. A write with no guard is the
// exact failure this mechanism exists to prevent: an agent overwriting a file
// it never looked at, or looked at before someone else changed it.
type MutationGuard struct {
	Kind    GuardKind
	Version FileVersion
}

// Validate accepts only the combinations that are actually checkable.
//
// A create guard carrying a version asserts something about a file it claims
// does not exist, and a replace guard without one is an unconditional
// overwrite wearing a guard's name. Both are refused here rather than at the
// filesystem, so a malformed guard can never reach a real file.
func (guard MutationGuard) Validate() error {
	switch guard.Kind {
	case GuardCreateIfAbsent:
		if guard.Version != "" {
			return argsError()
		}
		return nil
	case GuardReplaceIfVersion:
		if guard.Version == "" {
			return argsError()
		}
		return nil
	default:
		return argsError()
	}
}

// FileRead is what a read returns: the bytes, whether they were clipped, and
// the version those particular bytes were observed at.
//
// Truncated travels with the version on purpose. A guard derived from a
// truncated read would let an agent replace a whole file on the strength of
// having seen only its first page, so a caller that intends to mutate has to
// see that the observation was partial.
type FileRead struct {
	Data      []byte
	Truncated bool
	Version   FileVersion
}

// MutationOperation reports what a successful mutation actually did.
type MutationOperation string

const (
	MutationCreate MutationOperation = "create"
	MutationUpdate MutationOperation = "update"
)

// MutationResult is what a successful mutation returns.
//
// The new version is returned rather than left to be re-read: a caller that
// writes twice in a row would otherwise have to go back to the filesystem
// between the two, and the state it read back might not be the state it
// wrote.
type MutationResult struct {
	Version   FileVersion
	Operation MutationOperation
}
