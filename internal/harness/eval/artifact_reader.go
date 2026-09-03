package eval

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// errArtifactReaderMismatch marks an evidence file that changed shape
// since collection: a size or digest disagreement, a symlink, a hard
// link, or an escape from the evidence root. ArtifactReader refuses to
// read it rather than trust stale manifest metadata.
var errArtifactReaderMismatch = errors.New("eval: artifact reader: entry changed since collection")

// ArtifactReader reads evidence strictly from one Attempt's already-
// committed EvidenceManifest (design §14/§20): it opens only normalized,
// collected entries beneath the evidence root, re-verifying size and
// SHA-256 on every read rather than trusting the manifest's own recorded
// values, and refuses a symlink, a hard-linked file, or a type change
// since collection. It exposes no live database, workspace, network,
// Executor, Service, Provider, or unrestricted filesystem handle — only
// the exact bytes design §12 already committed as evidence.
type ArtifactReader struct {
	evidenceRoot string
	outcome      Outcome
	manifest     EvidenceManifest
}

// NewArtifactReader opens directories' already-published Outcome and
// EvidenceManifest (design §20: "Scoring begins only after a valid
// manifest commit marker exists") and cross-checks that the manifest's
// own OutcomeDigest still matches the published Outcome's canonical
// digest — a disagreement means the two documents no longer agree on
// which Outcome this manifest was built against, which this reader
// refuses to paper over.
func NewArtifactReader(directories AttemptRootDirectories) (*ArtifactReader, error) {
	outcome, err := ReadOutcome(directories.Root)
	if err != nil {
		return nil, fmt.Errorf("eval: new artifact reader: %w", err)
	}
	manifest, err := ReadEvidenceManifest(directories.Root)
	if err != nil {
		return nil, fmt.Errorf("eval: new artifact reader: %w", err)
	}
	outcomeDigest, err := OutcomeDigest(outcome)
	if err != nil {
		return nil, fmt.Errorf("eval: new artifact reader: digest outcome: %w", err)
	}
	if manifest.OutcomeDigest != outcomeDigest {
		return nil, fmt.Errorf("eval: new artifact reader: %w: manifest outcomeDigest %q disagrees with the published outcome's own digest %q",
			errArtifactReaderMismatch, manifest.OutcomeDigest, outcomeDigest)
	}
	return &ArtifactReader{evidenceRoot: directories.Evidence, outcome: outcome, manifest: manifest}, nil
}

// Outcome returns the immutable Outcome this reader was opened against.
func (reader *ArtifactReader) Outcome() Outcome { return reader.outcome }

// Manifest returns the immutable EvidenceManifest this reader was opened
// against.
func (reader *ArtifactReader) Manifest() EvidenceManifest { return reader.manifest }

// Entries returns every manifest entry with the given role, in manifest
// order, regardless of collection state — callers that only want usable
// evidence should filter for EntryCollected themselves or call ReadEntry,
// which refuses anything else.
func (reader *ArtifactReader) Entries(role string) []ManifestEntry {
	var entries []ManifestEntry
	for _, entry := range reader.manifest.Entries {
		if entry.Role == role {
			entries = append(entries, entry)
		}
	}
	return entries
}

// ReadEntry returns entryPath's exact bytes, re-verifying its size and
// SHA-256 against the manifest's own recorded values and refusing a
// symlink, a hard link, or any other non-regular file — even though
// CollectEvidence already enforced this once at collection time, a
// reader used later (potentially much later, by a separate regrade
// invocation) must not blindly trust metadata that could have changed
// since. entryPath must name a manifest entry in state EntryCollected;
// anything else is refused.
func (reader *ArtifactReader) ReadEntry(entryPath string) ([]byte, error) {
	var found *ManifestEntry
	for index := range reader.manifest.Entries {
		if reader.manifest.Entries[index].Path == entryPath {
			found = &reader.manifest.Entries[index]
			break
		}
	}
	if found == nil {
		return nil, fmt.Errorf("eval: artifact reader: %q: no such manifest entry", entryPath)
	}
	if found.State != EntryCollected {
		return nil, fmt.Errorf("eval: artifact reader: %q: not collected (state %q)", entryPath, found.State)
	}

	fullPath := filepath.Join(reader.evidenceRoot, filepath.FromSlash(entryPath))
	if !pathWithin(fullPath, reader.evidenceRoot) {
		return nil, fmt.Errorf("eval: artifact reader: %q: %w: escapes the evidence root", entryPath, errArtifactReaderMismatch)
	}
	info, err := os.Lstat(fullPath)
	if err != nil {
		return nil, fmt.Errorf("eval: artifact reader: %q: %w", entryPath, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("eval: artifact reader: %q: %w: no longer a regular file (%s)", entryPath, errArtifactReaderMismatch, info.Mode().Type())
	}
	links, err := hardLinkCount(fullPath, info)
	if err != nil {
		return nil, fmt.Errorf("eval: artifact reader: %q: check hard link count: %w", entryPath, err)
	}
	if links > 1 {
		return nil, fmt.Errorf("eval: artifact reader: %q: %w: hard-linked file", entryPath, errArtifactReaderMismatch)
	}
	if info.Size() != found.ByteLength {
		return nil, fmt.Errorf("eval: artifact reader: %q: %w: on-disk size %d disagrees with manifest %d",
			entryPath, errArtifactReaderMismatch, info.Size(), found.ByteLength)
	}

	data, err := os.ReadFile(fullPath)
	if err != nil {
		return nil, fmt.Errorf("eval: artifact reader: %q: %w", entryPath, err)
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != found.SHA256 {
		return nil, fmt.Errorf("eval: artifact reader: %q: %w: sha256 disagrees with manifest", entryPath, errArtifactReaderMismatch)
	}
	return data, nil
}
