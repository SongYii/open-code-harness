package eval

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// AttemptRootDirectories are design §8's isolated per-Attempt execution
// directories, plus the process/log directories design §2's Goals list
// under "isolate every Attempt's ... process resources": Workspace is the
// live, Subject-writable jailed filesystem seeded from the Scenario
// fixture; Database is the live canonical SQLite file's directory; Audit is
// the live JSONL audit export target; Process holds process-related
// resources (a subprocess working directory, PID/lease bookkeeping); Log
// holds raw captured stdout/stderr before the evidence collector finalizes
// them into evidence/stdout.log and evidence/stderr.log (design §12);
// Evidence is the final published evidence bundle design §12 describes.
type AttemptRootDirectories struct {
	Root      string
	Workspace string
	Database  string
	Audit     string
	Process   string
	Log       string
	Evidence  string
}

// NewAttemptRoot creates a fresh, empty isolated root under baseDirectory
// for one Attempt (design §8: "Each Attempt receives a new absolute root
// ... No resource is reused across Attempts, including repeated executions
// of the same Cell"). It fails if the root already exists, since a reused
// root would silently violate that guarantee.
func NewAttemptRoot(baseDirectory string, attemptID AttemptID) (AttemptRootDirectories, error) {
	if _, err := ParseAttemptID(string(attemptID)); err != nil {
		return AttemptRootDirectories{}, fmt.Errorf("eval: new attempt root: %w", err)
	}
	if !filepath.IsAbs(baseDirectory) {
		return AttemptRootDirectories{}, fmt.Errorf("eval: new attempt root: baseDirectory must be an absolute path")
	}
	root := filepath.Join(baseDirectory, string(attemptID))
	if err := os.Mkdir(root, 0o700); err != nil {
		return AttemptRootDirectories{}, fmt.Errorf("eval: new attempt root: %w", err)
	}
	directories := AttemptRootDirectories{
		Root:      root,
		Workspace: filepath.Join(root, "workspace"),
		Database:  filepath.Join(root, "database"),
		Audit:     filepath.Join(root, "audit"),
		Process:   filepath.Join(root, "process"),
		Log:       filepath.Join(root, "log"),
		Evidence:  filepath.Join(root, "evidence"),
	}
	for _, directory := range []string{
		directories.Workspace, directories.Database, directories.Audit,
		directories.Process, directories.Log, directories.Evidence,
	} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			return AttemptRootDirectories{}, fmt.Errorf("eval: new attempt root: %w", err)
		}
	}
	return directories, nil
}

// FixtureCopyLimits are the concrete, positive, already-resolved fixture
// copy bounds one CopyFixture call enforces (design §8/§19). A Scenario's
// own FixtureCopyPolicy only narrows an EvalSet's fixture limits (design
// §7); resolving that narrowing into concrete numbers is the caller's job
// (the runner, not yet implemented) — CopyFixture only enforces whatever
// bounds it is given, and refuses to guess a default for a value design
// does not fix.
type FixtureCopyLimits struct {
	MaxFiles      int
	MaxFileBytes  int64
	MaxTotalBytes int64
}

func (limits FixtureCopyLimits) validate() error {
	if limits.MaxFiles <= 0 {
		return fmt.Errorf("%w: maxFiles must be positive", errInvalidDocument)
	}
	if limits.MaxFileBytes <= 0 {
		return fmt.Errorf("%w: maxFileBytes must be positive", errInvalidDocument)
	}
	if limits.MaxTotalBytes <= 0 {
		return fmt.Errorf("%w: maxTotalBytes must be positive", errInvalidDocument)
	}
	return nil
}

// ResolveFixtureCopyLimits derives concrete FixtureCopyLimits for one
// Scenario within one EvalSet (design §7: "per-Scenario limits that may
// only narrow EvalSet limits"). MaxFiles narrows EvalSetLimits.FixtureFiles,
// the one design §19 default this package has for fixture copies; a
// Scenario's declared MaxFiles must not exceed it, and zero means "use the
// EvalSet default." MaxFileBytes and MaxTotalBytes have no EvalSet-level
// default in design §19's table (only a file *count* default exists there),
// so a Scenario must set both explicitly and positively -- this function
// fails closed rather than inventing an unstated default.
func ResolveFixtureCopyLimits(setLimits EvalSetLimits, policy FixtureCopyPolicy) (FixtureCopyLimits, error) {
	if err := policy.validate(); err != nil {
		return FixtureCopyLimits{}, fmt.Errorf("eval: resolve fixture copy limits: %w", err)
	}
	setLimits = setLimits.withDefaults()

	maxFiles := policy.MaxFiles
	switch {
	case maxFiles == 0:
		maxFiles = setLimits.FixtureFiles
	case maxFiles > setLimits.FixtureFiles:
		return FixtureCopyLimits{}, fmt.Errorf(
			"eval: resolve fixture copy limits: %w: fixtureCopyPolicy.maxFiles (%d) exceeds the EvalSet limit (%d)",
			errInvalidDocument, maxFiles, setLimits.FixtureFiles)
	}
	if policy.MaxFileBytes <= 0 {
		return FixtureCopyLimits{}, fmt.Errorf(
			"eval: resolve fixture copy limits: %w: fixtureCopyPolicy.maxFileBytes must be set (design §19 has no fixture byte default)",
			errInvalidDocument)
	}
	if policy.MaxTotalBytes <= 0 {
		return FixtureCopyLimits{}, fmt.Errorf(
			"eval: resolve fixture copy limits: %w: fixtureCopyPolicy.maxTotalBytes must be set (design §19 has no fixture byte default)",
			errInvalidDocument)
	}
	return FixtureCopyLimits{MaxFiles: maxFiles, MaxFileBytes: policy.MaxFileBytes, MaxTotalBytes: policy.MaxTotalBytes}, nil
}

// RefuseArtifactRootWithinFixture fails closed if artifactRoot and
// fixtureRoot nest inside one another in either direction (implementation
// plan Task 3 / design §26: "run refuses an artifact root inside a fixture
// workspace"). Live evidence must never land inside a checked-in fixture
// source tree, and a fixture source must never be pointed at a live
// artifact root either. Both paths must already be absolute and cleaned;
// this function does not resolve symlinks.
func RefuseArtifactRootWithinFixture(artifactRoot, fixtureRoot string) error {
	if !filepath.IsAbs(artifactRoot) || !filepath.IsAbs(fixtureRoot) {
		return fmt.Errorf("%w: artifactRoot and fixtureRoot must be absolute paths", errInvalidDocument)
	}
	artifactRoot = filepath.Clean(artifactRoot)
	fixtureRoot = filepath.Clean(fixtureRoot)
	if artifactRoot == fixtureRoot || pathWithin(artifactRoot, fixtureRoot) || pathWithin(fixtureRoot, artifactRoot) {
		return fmt.Errorf("%w: artifact root %q and fixture root %q must not nest within one another",
			errInvalidDocument, artifactRoot, fixtureRoot)
	}
	return nil
}

// pathWithin reports whether candidate is strictly inside root (candidate
// != root).
func pathWithin(candidate, root string) bool {
	relative, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	return relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

// FixtureCopyResult reports what CopyFixture actually copied.
type FixtureCopyResult struct {
	FileCount  int
	TotalBytes int64
}

// errFixtureRejected marks a fixture entry CopyFixture refuses to copy at
// all, rather than skip (design §8): a symlink, a hard-linked file, a
// socket, a device, a FIFO, or any other non-regular, non-directory type.
var errFixtureRejected = errors.New("eval: fixture entry rejected")

// CopyFixture copies every directory and regular file under
// sourceDirectory into destinationDirectory, enforcing design §8's fixture
// isolation discipline before the Subject ever starts. It does not follow
// symlinks (a symlinked directory is reported as a symlink entry, never
// descended into) and rejects outright — never silently skips — a symlink,
// a hard-linked file (link count > 1), a socket, a device, a FIFO, or any
// other unsupported file type, as well as any entry whose relative path is
// not already a normalized, contained, relative path. Only a regular file's
// executable bit and the directory structure are preserved; no other
// permission or ownership bit carries over. destinationDirectory must
// already exist and be empty.
func CopyFixture(sourceDirectory, destinationDirectory string, limits FixtureCopyLimits) (FixtureCopyResult, error) {
	if err := limits.validate(); err != nil {
		return FixtureCopyResult{}, fmt.Errorf("eval: copy fixture: %w", err)
	}
	if !filepath.IsAbs(sourceDirectory) || !filepath.IsAbs(destinationDirectory) {
		return FixtureCopyResult{}, fmt.Errorf("eval: copy fixture: source and destination must be absolute paths")
	}
	if err := requireEmptyDirectory(destinationDirectory); err != nil {
		return FixtureCopyResult{}, fmt.Errorf("eval: copy fixture: %w", err)
	}

	var result FixtureCopyResult
	walkErr := filepath.WalkDir(sourceDirectory, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("eval: copy fixture: walk %s: %w", path, walkErr)
		}
		relative, err := filepath.Rel(sourceDirectory, path)
		if err != nil {
			return fmt.Errorf("eval: copy fixture: relative path for %s: %w", path, err)
		}
		if relative == "." {
			return nil // the source root itself; already validated absolute above.
		}
		relative = filepath.ToSlash(relative)
		if err := validateContainedRelativePath(relative); err != nil {
			return fmt.Errorf("eval: copy fixture: %s: %w", relative, err)
		}
		info, err := entry.Info() // Lstat-based: reports a symlink as a symlink, never dereferences it.
		if err != nil {
			return fmt.Errorf("eval: copy fixture: stat %s: %w", relative, err)
		}
		destinationPath := filepath.Join(destinationDirectory, filepath.FromSlash(relative))

		if entry.IsDir() {
			if err := os.Mkdir(destinationPath, 0o700); err != nil {
				return fmt.Errorf("eval: copy fixture: mkdir %s: %w", relative, err)
			}
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("eval: copy fixture: %s: %w: unsupported file type %s", relative, errFixtureRejected, info.Mode().Type())
		}
		links, err := hardLinkCount(path, info)
		if err != nil {
			return fmt.Errorf("eval: copy fixture: %s: check hard link count: %w", relative, err)
		}
		if links > 1 {
			return fmt.Errorf("eval: copy fixture: %s: %w: hard-linked file", relative, errFixtureRejected)
		}

		result.FileCount++
		if result.FileCount > limits.MaxFiles {
			return fmt.Errorf("eval: copy fixture: exceeds limits.maxFiles (%d)", limits.MaxFiles)
		}
		if info.Size() > limits.MaxFileBytes {
			return fmt.Errorf("eval: copy fixture: %s: exceeds limits.maxFileBytes (%d)", relative, limits.MaxFileBytes)
		}
		result.TotalBytes += info.Size()
		if result.TotalBytes > limits.MaxTotalBytes {
			return fmt.Errorf("eval: copy fixture: exceeds limits.maxTotalBytes (%d)", limits.MaxTotalBytes)
		}
		if err := copyRegularFile(path, destinationPath, info); err != nil {
			return fmt.Errorf("eval: copy fixture: %s: %w", relative, err)
		}
		return nil
	})
	if walkErr != nil {
		return FixtureCopyResult{}, walkErr
	}
	return result, nil
}

func requireEmptyDirectory(directory string) error {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return fmt.Errorf("destination: %w", err)
	}
	if len(entries) != 0 {
		return fmt.Errorf("destination %q is not empty", directory)
	}
	return nil
}

// hardLinkCount (implementation plan Task 3) is defined per-platform in
// fixture_stat_unix.go and fixture_stat_windows.go, so this file stays free
// of any platform-specific stat type and GOOS=windows go build/test still
// compiles this package.

// copyRegularFile copies one already-validated regular file's bytes,
// preserving only whether it was executable (design §8: "preserves only
// regular-file executable bits and directory structure").
func copyRegularFile(sourcePath, destinationPath string, info fs.FileInfo) error {
	source, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer source.Close()

	mode := os.FileMode(0o600)
	if info.Mode()&0o111 != 0 {
		mode = 0o700
	}
	destination, err := os.OpenFile(destinationPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(destination, source); err != nil {
		_ = destination.Close()
		return err
	}
	return destination.Close()
}
