package eval

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

// Filenames and subdirectory names design §12 fixes for one Attempt's
// publication root.
const (
	attemptFilename       = "attempt.json"
	outcomeFilename       = "outcome.json"
	evidenceDirectoryName = "evidence"
	manifestFilename      = "manifest.json"
	scoresDirectoryName   = "scores"
	tempNamePrefix        = ".eval.tmp."
)

// errAlreadyPublished means the target document already exists. Every
// document in this package is written at most once (design §12: "attempt.json
// is atomically published before Subject startup and is immutable",
// "outcome.json is atomically published at most once", the manifest "is the
// commit marker for a scoreable Attempt", "A Score publishes into a new path
// and never replaces another Score").
var errAlreadyPublished = errors.New("eval: already published")

// PublishAttempt atomically publishes attempt.json into directory. It fails
// with errAlreadyPublished if attempt.json already exists; it never
// overwrites one.
func PublishAttempt(directory string, attempt Attempt) error {
	if err := attempt.Validate(); err != nil {
		return fmt.Errorf("eval: publish attempt: %w", err)
	}
	return publishDocument(directory, attemptFilename, attempt)
}

// ReadAttempt reads and validates the attempt.json published in directory.
func ReadAttempt(directory string) (Attempt, error) {
	data, err := os.ReadFile(filepath.Join(directory, attemptFilename))
	if err != nil {
		return Attempt{}, fmt.Errorf("eval: read attempt: %w", err)
	}
	return DecodeAttempt(data)
}

// PublishOutcome atomically publishes outcome.json into directory. It fails
// with errAlreadyPublished if outcome.json already exists.
func PublishOutcome(directory string, outcome Outcome) error {
	if err := outcome.Validate(); err != nil {
		return fmt.Errorf("eval: publish outcome: %w", err)
	}
	return publishDocument(directory, outcomeFilename, outcome)
}

// ReadOutcome reads and validates the outcome.json published in directory.
func ReadOutcome(directory string) (Outcome, error) {
	data, err := os.ReadFile(filepath.Join(directory, outcomeFilename))
	if err != nil {
		return Outcome{}, fmt.Errorf("eval: read outcome: %w", err)
	}
	return DecodeOutcome(data)
}

// PublishEvidenceManifest atomically publishes evidence/manifest.json into
// directory — the commit marker for a scoreable Attempt (design §12). The
// caller is responsible for having already published every evidence file
// the manifest's entries reference; this function does not inspect them.
// It fails with errAlreadyPublished if the manifest already exists.
func PublishEvidenceManifest(directory string, manifest EvidenceManifest) error {
	if err := manifest.Validate(); err != nil {
		return fmt.Errorf("eval: publish evidence manifest: %w", err)
	}
	evidenceDirectory := filepath.Join(directory, evidenceDirectoryName)
	if err := os.MkdirAll(evidenceDirectory, 0o700); err != nil {
		return fmt.Errorf("eval: publish evidence manifest: create evidence directory: %w", err)
	}
	return publishDocument(evidenceDirectory, manifestFilename, manifest)
}

// ReadEvidenceManifest reads and validates the evidence/manifest.json
// published in directory. Its presence is what makes an Attempt scoreable
// (design §20: "Scoring begins only after a valid manifest commit marker
// exists").
func ReadEvidenceManifest(directory string) (EvidenceManifest, error) {
	data, err := os.ReadFile(filepath.Join(directory, evidenceDirectoryName, manifestFilename))
	if err != nil {
		return EvidenceManifest{}, fmt.Errorf("eval: read evidence manifest: %w", err)
	}
	return DecodeEvidenceManifest(data)
}

// PublishScore atomically publishes one Score into directory/scores/, named
// by the Score's own ID. It never replaces another Score; publishing the
// same Score ID twice fails with errAlreadyPublished.
func PublishScore(directory string, score Score) error {
	if err := score.Validate(); err != nil {
		return fmt.Errorf("eval: publish score: %w", err)
	}
	scoresDirectory := filepath.Join(directory, scoresDirectoryName)
	if err := os.MkdirAll(scoresDirectory, 0o700); err != nil {
		return fmt.Errorf("eval: publish score: create scores directory: %w", err)
	}
	return publishDocument(scoresDirectory, string(score.ID)+".json", score)
}

// ReadScores reads and validates every Score published under
// directory/scores/, in deterministic (filename) order.
func ReadScores(directory string) ([]Score, error) {
	scoresDirectory := filepath.Join(directory, scoresDirectoryName)
	entries, err := os.ReadDir(scoresDirectory)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("eval: read scores: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	scores := make([]Score, 0, len(names))
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(scoresDirectory, name))
		if err != nil {
			return nil, fmt.Errorf("eval: read score %s: %w", name, err)
		}
		score, err := DecodeScore(data)
		if err != nil {
			return nil, fmt.Errorf("eval: read score %s: %w", name, err)
		}
		scores = append(scores, score)
	}
	return scores, nil
}

// The five OS-call seams publishDocument drives, plus syncDirectoryFn below,
// give a fault-injection test exactly one substitutable point per design
// §12 publication stage: temp create, (partial) write, file sync, close,
// no-overwrite rename, and directory sync. Matching composition/assembly.go's
// own checkSandboxAvailability convention, production never reassigns
// these; only publish_fault_test.go does, always restoring the original
// before returning.
var (
	createTempFile = os.CreateTemp
	writeTempFile  = func(file *os.File, data []byte) (int, error) { return file.Write(data) }
	syncTempFile   = func(file *os.File) error { return file.Sync() }
	closeTempFile  = func(file *os.File) error { return file.Close() }
	linkTempFile   = os.Link
)

// publishDocument marshals value as canonical JSON and publishes it into
// directory/filename at most once (design §12): a same-directory temporary
// file, bounded to this one document's bytes, synced, closed, then linked
// into its final name so a concurrent or repeated publish of the same
// filename fails instead of silently overwriting, then the directory itself
// is synced where the platform supports it. Any failure leaves either the
// prior state (if the target already existed) or an uncommitted, distinctly
// named temp file — never a partially written target. A failure after temp
// creation always attempts to remove that exact temp file; if the process
// dies before that cleanup runs, CleanupStaleTempFiles recognizes and
// removes it later by its distinct naming scheme alone.
func publishDocument(directory, filename string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("eval: encode %s: %w", filename, err)
	}
	finalPath := filepath.Join(directory, filename)
	tempFile, err := createTempFile(directory, tempNamePrefix+filename+".*")
	if err != nil {
		return fmt.Errorf("eval: create temp file for %s: %w", filename, err)
	}
	tempPath := tempFile.Name()
	defer os.Remove(tempPath) // no-op once the link below succeeds and this is a stale temp name
	if _, err := writeTempFile(tempFile, data); err != nil {
		_ = closeTempFile(tempFile)
		return fmt.Errorf("eval: write %s: %w", filename, err)
	}
	if err := syncTempFile(tempFile); err != nil {
		_ = closeTempFile(tempFile)
		return fmt.Errorf("eval: sync %s: %w", filename, err)
	}
	if err := closeTempFile(tempFile); err != nil {
		return fmt.Errorf("eval: close %s: %w", filename, err)
	}
	if err := linkTempFile(tempPath, finalPath); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("%w: %s", errAlreadyPublished, filename)
		}
		return fmt.Errorf("eval: publish %s: %w", filename, err)
	}
	if err := syncDirectoryFn(directory); err != nil {
		return fmt.Errorf("eval: sync directory after publishing %s: %w", filename, err)
	}
	return nil
}

// StaleTempFile is one eval-owned uncommitted temp file
// CleanupStaleTempFiles found and removed.
type StaleTempFile struct {
	Path string
	Size int64
}

// CleanupStaleTempFiles walks root recursively and removes only files whose
// name carries this package's exact temp-naming scheme (tempNamePrefix,
// design §12: "Startup removes only eval-owned temp names after recording
// diagnostics; it never guesses that an uncommitted file was complete"). It
// never removes, inspects the content of, or otherwise touches any other
// file, including a legitimately in-progress temp file from a still-running
// process -- callers run this only at startup, before any new publish can
// begin. It returns every file it removed, bounded by root's own contents,
// as the recorded diagnostic.
func CleanupStaleTempFiles(root string) ([]StaleTempFile, error) {
	var removed []StaleTempFile
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), tempNamePrefix) {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("eval: cleanup stale temp file %s: %w", path, err)
		}
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("eval: cleanup stale temp file %s: %w", path, err)
		}
		removed = append(removed, StaleTempFile{Path: path, Size: info.Size()})
		return nil
	})
	if err != nil {
		return removed, err
	}
	return removed, nil
}

// syncDirectoryFn is the sixth fault-injection seam (directory sync).
var syncDirectoryFn = syncDirectory

// syncDirectory fsyncs directory so the rename above survives a crash. Not
// every filesystem supports fsync on a directory descriptor; design §12
// says to do this "where the platform supports it", so that specific,
// well-known unsupported-operation error is not fatal.
func syncDirectory(directory string) error {
	dir, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer dir.Close()
	err = dir.Sync()
	if err == nil || errors.Is(err, syscall.ENOTSUP) || errors.Is(err, syscall.EINVAL) {
		return nil
	}
	return err
}
