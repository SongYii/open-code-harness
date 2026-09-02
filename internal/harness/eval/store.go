package eval

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
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

// publishDocument marshals value as canonical JSON and publishes it into
// directory/filename at most once (design §12): a same-directory temporary
// file, bounded to this one document's bytes, synced, closed, then linked
// into its final name so a concurrent or repeated publish of the same
// filename fails instead of silently overwriting, then the directory itself
// is synced where the platform supports it. Any failure leaves either the
// prior state (if the target already existed) or an uncommitted, distinctly
// named temp file — never a partially written target.
func publishDocument(directory, filename string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("eval: encode %s: %w", filename, err)
	}
	finalPath := filepath.Join(directory, filename)
	tempFile, err := os.CreateTemp(directory, tempNamePrefix+filename+".*")
	if err != nil {
		return fmt.Errorf("eval: create temp file for %s: %w", filename, err)
	}
	tempPath := tempFile.Name()
	defer os.Remove(tempPath) // no-op once the link below succeeds and this is a stale temp name
	if _, err := tempFile.Write(data); err != nil {
		_ = tempFile.Close()
		return fmt.Errorf("eval: write %s: %w", filename, err)
	}
	if err := tempFile.Sync(); err != nil {
		_ = tempFile.Close()
		return fmt.Errorf("eval: sync %s: %w", filename, err)
	}
	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("eval: close %s: %w", filename, err)
	}
	if err := os.Link(tempPath, finalPath); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("%w: %s", errAlreadyPublished, filename)
		}
		return fmt.Errorf("eval: publish %s: %w", filename, err)
	}
	if err := syncDirectory(directory); err != nil {
		return fmt.Errorf("eval: sync directory after publishing %s: %w", filename, err)
	}
	return nil
}

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
