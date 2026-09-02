package eval

import (
	"fmt"
	"path"
	"strings"
	"time"
)

// ManifestEntryState is one artifact's collection outcome (design §14).
type ManifestEntryState string

const (
	EntryCollected ManifestEntryState = "collected"
	EntryMissing   ManifestEntryState = "missing"
	EntryTruncated ManifestEntryState = "truncated"
	EntryRejected  ManifestEntryState = "rejected"
)

// ManifestEntry is one artifact inventory row (design §14).
type ManifestEntry struct {
	Path      string `json:"path"`
	Role      string `json:"role"`
	MediaType string `json:"mediaType"`

	// SHA256 and ByteLength are present only when State is EntryCollected.
	SHA256     string `json:"sha256,omitempty"`
	ByteLength int64  `json:"byteLength,omitempty"`

	Required bool               `json:"required"`
	State    ManifestEntryState `json:"state"`

	// ReasonCode and Detail are present only when State is not
	// EntryCollected (design §14: "stable reason code and bounded safe
	// detail when not collected").
	ReasonCode string `json:"reasonCode,omitempty"`
	Detail     string `json:"detail,omitempty"`

	// ProducedBy names the producing step or verifier identity when
	// applicable.
	ProducedBy string `json:"producedBy,omitempty"`
}

// EvidenceManifest is the frozen `och.eval.evidence-manifest` document: the
// only artifact inventory a scorer may read (design §4/§14). It is
// published last, atomically, after evidence collection completes and is
// the commit marker for a scoreable Attempt (design §12). The manifest does
// not hash itself; a Score records the SHA-256 of the exact published
// manifest bytes separately (see DigestEvidenceManifest).
type EvidenceManifest struct {
	FormatVersion int       `json:"formatVersion"`
	Schema        string    `json:"schema"`
	AttemptID     AttemptID `json:"attemptId"`
	OutcomeDigest Digest    `json:"outcomeDigest"`

	Entries []ManifestEntry `json:"entries"`

	TotalBytes int64 `json:"totalBytes"`
	FileCount  int   `json:"fileCount"`

	CollectionStartedAt time.Time `json:"collectionStartedAt"`
	CollectionEndedAt   time.Time `json:"collectionEndedAt"`

	// CollectionDiagnosticsDigest is the digest of a separate bounded
	// diagnostics document (design §14/§19), not implemented in this
	// package yet; the field is retained as an opaque digest reference.
	CollectionDiagnosticsDigest string `json:"collectionDiagnosticsDigest,omitempty"`
}

// DecodeEvidenceManifest strictly decodes and validates one
// `och.eval.evidence-manifest` document (design §6).
func DecodeEvidenceManifest(data []byte) (EvidenceManifest, error) {
	var manifest EvidenceManifest
	if err := decodeStrict(data, &manifest); err != nil {
		return EvidenceManifest{}, fmt.Errorf("eval: evidence manifest: %w", err)
	}
	if manifest.Schema != SchemaEvidenceManifest {
		return EvidenceManifest{}, fmt.Errorf("eval: evidence manifest: %w: %q", errUnsupportedSchema, manifest.Schema)
	}
	if manifest.FormatVersion != FormatVersion {
		return EvidenceManifest{}, fmt.Errorf("eval: evidence manifest: %w: %d", errUnsupportedFormatVersion, manifest.FormatVersion)
	}
	if err := manifest.Validate(); err != nil {
		return EvidenceManifest{}, err
	}
	return manifest, nil
}

// Validate checks every field this document requires, including that every
// entry path is normalized and contained (design §8's path-containment
// discipline applies just as much to reading evidence back as it does to
// collecting it). It does not check that every Scenario-required role
// arrived — that is a scoring-time fail-closed check (design §6/§20), not a
// document-shape check, because a manifest recording a missing required
// role is itself a valid, honestly-collected manifest.
func (manifest EvidenceManifest) Validate() error {
	if _, err := ParseAttemptID(string(manifest.AttemptID)); err != nil {
		return fmt.Errorf("%w: %w", errInvalidDocument, err)
	}
	if !digestStringPattern.MatchString(string(manifest.OutcomeDigest)) {
		return fmt.Errorf("%w: outcomeDigest must be sha256:<64 lowercase hex>", errInvalidDocument)
	}
	if len(manifest.Entries) == 0 {
		return fmt.Errorf("%w: at least one entry is required", errInvalidDocument)
	}
	seenPaths := make(map[string]struct{}, len(manifest.Entries))
	for index, entry := range manifest.Entries {
		if err := entry.validate(index); err != nil {
			return err
		}
		if _, exists := seenPaths[entry.Path]; exists {
			return fmt.Errorf("%w: entry %d: duplicate path %q", errInvalidDocument, index, entry.Path)
		}
		seenPaths[entry.Path] = struct{}{}
	}
	if manifest.TotalBytes < 0 {
		return fmt.Errorf("%w: totalBytes must not be negative", errInvalidDocument)
	}
	if manifest.FileCount < 0 {
		return fmt.Errorf("%w: fileCount must not be negative", errInvalidDocument)
	}
	if manifest.CollectionStartedAt.IsZero() {
		return fmt.Errorf("%w: collectionStartedAt is required", errInvalidDocument)
	}
	if manifest.CollectionEndedAt.IsZero() {
		return fmt.Errorf("%w: collectionEndedAt is required", errInvalidDocument)
	}
	if manifest.CollectionEndedAt.Before(manifest.CollectionStartedAt) {
		return fmt.Errorf("%w: collectionEndedAt must not precede collectionStartedAt", errInvalidDocument)
	}
	if manifest.CollectionDiagnosticsDigest != "" && !digestStringPattern.MatchString(manifest.CollectionDiagnosticsDigest) {
		return fmt.Errorf("%w: collectionDiagnosticsDigest must be sha256:<64 lowercase hex>", errInvalidDocument)
	}
	return nil
}

func (entry ManifestEntry) validate(index int) error {
	if err := validateContainedRelativePath(entry.Path); err != nil {
		return fmt.Errorf("%w: entry %d: path: %w", errInvalidDocument, index, err)
	}
	if !hasText(entry.Role) {
		return fmt.Errorf("%w: entry %d: role is required", errInvalidDocument, index)
	}
	if !hasText(entry.MediaType) {
		return fmt.Errorf("%w: entry %d: mediaType is required", errInvalidDocument, index)
	}
	switch entry.State {
	case EntryCollected:
		if !sha256HexPattern.MatchString(entry.SHA256) {
			return fmt.Errorf("%w: entry %d: sha256 must be 64 lowercase hex characters when collected", errInvalidDocument, index)
		}
		if entry.ByteLength < 0 {
			return fmt.Errorf("%w: entry %d: byteLength must not be negative", errInvalidDocument, index)
		}
		if hasText(entry.ReasonCode) {
			return fmt.Errorf("%w: entry %d: reasonCode must be empty when collected", errInvalidDocument, index)
		}
	case EntryMissing, EntryTruncated, EntryRejected:
		if !hasText(entry.ReasonCode) {
			return fmt.Errorf("%w: entry %d: reasonCode is required when not collected", errInvalidDocument, index)
		}
	default:
		return fmt.Errorf("%w: entry %d: unknown state %q", errInvalidDocument, index, entry.State)
	}
	return nil
}

// validateContainedRelativePath enforces design §8's fixture/evidence path
// discipline on a manifest entry path: normalized, relative, and unable to
// escape the Attempt's evidence root.
func validateContainedRelativePath(entryPath string) error {
	if !hasText(entryPath) {
		return fmt.Errorf("%w: must not be empty", errInvalidDocument)
	}
	if path.IsAbs(entryPath) {
		return fmt.Errorf("%w: must be relative", errInvalidDocument)
	}
	cleaned := path.Clean(entryPath)
	if cleaned != entryPath {
		return fmt.Errorf("%w: must already be in normalized form (got %q, want %q)", errInvalidDocument, entryPath, cleaned)
	}
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return fmt.Errorf("%w: must not escape its root", errInvalidDocument)
	}
	return nil
}
