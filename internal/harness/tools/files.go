package tools

// FileVersion is an opaque version token for an observed filesystem target.
type FileVersion string

const MaxEditFileBytes = 1 << 20

// GuardKind identifies the mutation precondition required by a filesystem operation.
type GuardKind string

const (
	GuardCreateIfAbsent   GuardKind = "create_if_absent"
	GuardReplaceIfVersion GuardKind = "replace_if_version"
)

// MutationGuard carries the precondition for a filesystem mutation.
type MutationGuard struct {
	Kind    GuardKind
	Version FileVersion
}

func (guard MutationGuard) Validate() error {
	switch guard.Kind {
	case GuardCreateIfAbsent:
		if guard.Version == "" {
			return nil
		}
	case GuardReplaceIfVersion:
		if guard.Version != "" {
			return nil
		}
	}
	return argsError()
}

type FileRead struct {
	Data      []byte
	Truncated bool
	Version   FileVersion
}

type MutationOperation string

const (
	MutationCreate MutationOperation = "create"
	MutationUpdate MutationOperation = "update"
)

type MutationResult struct {
	Version   FileVersion
	Operation MutationOperation
}
