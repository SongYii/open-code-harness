package eval

import (
	"reflect"
	"testing"
	"time"
)

func validEvidenceManifest(t *testing.T, attemptID AttemptID) EvidenceManifest {
	t.Helper()
	started := time.Date(2026, 9, 2, 12, 1, 30, 0, time.UTC)
	return EvidenceManifest{
		FormatVersion: FormatVersion,
		Schema:        SchemaEvidenceManifest,
		AttemptID:     attemptID,
		OutcomeDigest: mustDigest(t, 4),
		Entries: []ManifestEntry{
			{
				Path:       "transcript.jsonl",
				Role:       "transcript",
				MediaType:  "application/jsonl",
				SHA256:     repeatHex(5),
				ByteLength: 1024,
				Required:   true,
				State:      EntryCollected,
			},
			{
				Path:       "workspace/output.txt",
				Role:       "workspace",
				MediaType:  "text/plain",
				Required:   false,
				State:      EntryMissing,
				ReasonCode: "not_produced",
				Detail:     "scenario did not request collection",
			},
		},
		TotalBytes:          1024,
		FileCount:           1,
		CollectionStartedAt: started,
		CollectionEndedAt:   started.Add(2 * time.Second),
	}
}

func TestDecodeEvidenceManifestRoundTrip(t *testing.T) {
	want := validEvidenceManifest(t, mustAttemptID(t))
	got, err := DecodeEvidenceManifest(marshal(t, want))
	if err != nil {
		t.Fatalf("DecodeEvidenceManifest: %v", err)
	}
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("DecodeEvidenceManifest round trip mismatch:\nwant %+v\ngot  %+v", want, got)
	}
}

func TestEvidenceManifestValidateRejectsEmptyEntries(t *testing.T) {
	manifest := validEvidenceManifest(t, mustAttemptID(t))
	manifest.Entries = nil
	if err := manifest.Validate(); err == nil {
		t.Fatal("Validate() accepted an empty entries list")
	}
}

func TestEvidenceManifestValidateRejectsDuplicatePath(t *testing.T) {
	manifest := validEvidenceManifest(t, mustAttemptID(t))
	manifest.Entries = append(manifest.Entries, manifest.Entries[0])
	if err := manifest.Validate(); err == nil {
		t.Fatal("Validate() accepted a duplicate entry path")
	}
}

func TestEvidenceManifestValidateRejectsCollectedWithoutDigest(t *testing.T) {
	manifest := validEvidenceManifest(t, mustAttemptID(t))
	manifest.Entries[0].SHA256 = ""
	if err := manifest.Validate(); err == nil {
		t.Fatal("Validate() accepted a collected entry without a sha256")
	}
}

func TestEvidenceManifestValidateRejectsMissingReasonCode(t *testing.T) {
	manifest := validEvidenceManifest(t, mustAttemptID(t))
	manifest.Entries[1].ReasonCode = ""
	if err := manifest.Validate(); err == nil {
		t.Fatal("Validate() accepted a non-collected entry without a reasonCode")
	}
}

func TestValidateContainedRelativePathRejectsEscape(t *testing.T) {
	tests := []string{"", "/abs/path", "../escape", "a/../../b", "a/../b"}
	for _, path := range tests {
		if err := validateContainedRelativePath(path); err == nil {
			t.Fatalf("validateContainedRelativePath(%q) accepted, want error", path)
		}
	}
}

func TestValidateContainedRelativePathAcceptsNormalPaths(t *testing.T) {
	tests := []string{"a.txt", "a/b.txt", "a/b/c.txt"}
	for _, path := range tests {
		if err := validateContainedRelativePath(path); err != nil {
			t.Fatalf("validateContainedRelativePath(%q) rejected: %v", path, err)
		}
	}
}

func TestEvidenceManifestValidateRejectsEndBeforeStartCollection(t *testing.T) {
	manifest := validEvidenceManifest(t, mustAttemptID(t))
	manifest.CollectionEndedAt = manifest.CollectionStartedAt.Add(-time.Second)
	if err := manifest.Validate(); err == nil {
		t.Fatal("Validate() accepted collectionEndedAt before collectionStartedAt")
	}
}
