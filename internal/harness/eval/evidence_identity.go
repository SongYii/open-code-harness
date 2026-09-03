package eval

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// EvidenceDocuments are the frozen identity documents that must accompany
// every scoreable Attempt (design §14). Attempt identity binds the other
// three documents by digest; collection stages all four into the manifest.
type EvidenceDocuments struct {
	Scenario Scenario
	Subject  Subject
	Executor Executor
	Attempt  Attempt
}

func (documents EvidenceDocuments) validate() error {
	if err := documents.Scenario.Validate(); err != nil {
		return fmt.Errorf("scenario: %w", err)
	}
	if err := documents.Subject.Validate(); err != nil {
		return fmt.Errorf("subject: %w", err)
	}
	if err := documents.Executor.Validate(); err != nil {
		return fmt.Errorf("executor: %w", err)
	}
	if err := documents.Attempt.Validate(); err != nil {
		return fmt.Errorf("attempt: %w", err)
	}
	if documents.Attempt.ScenarioID != documents.Scenario.ID {
		return fmt.Errorf("attempt scenarioId %q disagrees with scenario %q", documents.Attempt.ScenarioID, documents.Scenario.ID)
	}
	if documents.Attempt.SubjectID != documents.Subject.ID {
		return fmt.Errorf("attempt subjectId %q disagrees with subject %q", documents.Attempt.SubjectID, documents.Subject.ID)
	}
	if documents.Attempt.ExecutorID != documents.Executor.ID {
		return fmt.Errorf("attempt executorId %q disagrees with executor %q", documents.Attempt.ExecutorID, documents.Executor.ID)
	}
	scenarioDigest, err := ScenarioDigest(documents.Scenario)
	if err != nil {
		return err
	}
	subjectDigest, err := SubjectDigest(documents.Subject)
	if err != nil {
		return err
	}
	executorDigest, err := ExecutorDigest(documents.Executor)
	if err != nil {
		return err
	}
	if documents.Attempt.ScenarioDigest != scenarioDigest {
		return fmt.Errorf("attempt scenarioDigest %q disagrees with frozen scenario %q", documents.Attempt.ScenarioDigest, scenarioDigest)
	}
	if documents.Attempt.SubjectDigest != subjectDigest {
		return fmt.Errorf("attempt subjectDigest %q disagrees with frozen subject %q", documents.Attempt.SubjectDigest, subjectDigest)
	}
	if documents.Attempt.ExecutorDigest != executorDigest {
		return fmt.Errorf("attempt executorDigest %q disagrees with frozen executor %q", documents.Attempt.ExecutorDigest, executorDigest)
	}
	return nil
}

func stageEvidenceDocuments(directories AttemptRootDirectories, documents EvidenceDocuments, budget *collectionBudget) error {
	if err := documents.validate(); err != nil {
		return fmt.Errorf("validate frozen evidence documents: %w", err)
	}

	publishedAttempt, err := os.ReadFile(filepath.Join(directories.Root, attemptFilename))
	if err != nil {
		return fmt.Errorf("read published attempt: %w", err)
	}
	attemptBytes, err := json.Marshal(documents.Attempt)
	if err != nil {
		return fmt.Errorf("encode frozen attempt: %w", err)
	}
	if !bytes.Equal(publishedAttempt, attemptBytes) {
		return fmt.Errorf("published attempt bytes disagree with the frozen Attempt supplied to collection")
	}

	values := []struct {
		path string
		role string
		data []byte
	}{
		{path: "scenario.json", role: "scenario"},
		{path: "subject.json", role: "subject"},
		{path: "executor.json", role: "executor"},
		{path: "attempt.json", role: "attempt", data: publishedAttempt},
	}
	values[0].data, err = json.Marshal(documents.Scenario)
	if err != nil {
		return fmt.Errorf("encode frozen scenario: %w", err)
	}
	values[1].data, err = json.Marshal(documents.Subject)
	if err != nil {
		return fmt.Errorf("encode frozen subject: %w", err)
	}
	values[2].data, err = json.Marshal(documents.Executor)
	if err != nil {
		return fmt.Errorf("encode frozen executor: %w", err)
	}

	for _, value := range values {
		staged, err := stageIdentityDocument(filepath.Join(directories.Evidence, value.path), value.data)
		if err != nil {
			return fmt.Errorf("stage %s: %w", value.path, err)
		}
		budget.track(ManifestEntry{
			Path: value.path, Role: value.role, MediaType: "application/json",
			Required: true, State: EntryCollected, SHA256: staged.sha256, ByteLength: staged.byteLength,
			ProducedBy: "och-eval",
		})
	}
	return nil
}

func stageIdentityDocument(destination string, data []byte) (stagedArtifact, error) {
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if !os.IsExist(err) {
			return stagedArtifact{}, err
		}
		info, statErr := os.Lstat(destination)
		if statErr != nil {
			return stagedArtifact{}, statErr
		}
		if !info.Mode().IsRegular() {
			return stagedArtifact{}, fmt.Errorf("existing staged identity is not a regular file")
		}
		links, linkErr := hardLinkCount(destination, info)
		if linkErr != nil {

			return stagedArtifact{}, linkErr
		}
		if links > 1 {
			return stagedArtifact{}, fmt.Errorf("existing staged identity is hard-linked")
		}
		existing, readErr := os.ReadFile(destination)
		if readErr != nil {
			return stagedArtifact{}, readErr
		}
		if !bytes.Equal(existing, data) {
			return stagedArtifact{}, fmt.Errorf("existing staged identity bytes disagree")
		}
		return digestFile(destination)
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return stagedArtifact{}, err
	}
	if err := file.Close(); err != nil {
		return stagedArtifact{}, err
	}
	sum := sha256.Sum256(data)
	return stagedArtifact{sha256: hex.EncodeToString(sum[:]), byteLength: int64(len(data))}, nil
}

func readEvidenceDocuments(reader *ArtifactReader, publishedAttempt Attempt) (EvidenceDocuments, EvalLane, error) {
	readOne := func(role string) ([]byte, error) {
		entries := reader.Entries(role)
		if len(entries) != 1 || entries[0].State != EntryCollected {
			return nil, fmt.Errorf("frozen %s evidence must contain exactly one collected entry", role)
		}
		return reader.ReadEntry(entries[0].Path)
	}

	scenarioBytes, err := readOne("scenario")
	if err != nil {
		return EvidenceDocuments{}, "", err
	}
	scenario, err := DecodeScenario(scenarioBytes)
	if err != nil {
		return EvidenceDocuments{}, "", err
	}
	subjectBytes, err := readOne("subject")
	if err != nil {
		return EvidenceDocuments{}, "", err
	}
	subject, err := DecodeSubject(subjectBytes)
	if err != nil {
		return EvidenceDocuments{}, "", err
	}
	executorBytes, err := readOne("executor")
	if err != nil {
		return EvidenceDocuments{}, "", err
	}
	executor, err := DecodeExecutor(executorBytes)
	if err != nil {
		return EvidenceDocuments{}, "", err
	}
	attemptBytes, err := readOne("attempt")
	if err != nil {
		return EvidenceDocuments{}, "", err
	}
	attempt, err := DecodeAttempt(attemptBytes)
	if err != nil {
		return EvidenceDocuments{}, "", err
	}

	publishedBytes, err := json.Marshal(publishedAttempt)
	if err != nil {
		return EvidenceDocuments{}, "", err
	}
	if !bytes.Equal(attemptBytes, publishedBytes) {
		return EvidenceDocuments{}, "", fmt.Errorf("manifest Attempt evidence disagrees with published attempt.json")
	}
	documents := EvidenceDocuments{Scenario: scenario, Subject: subject, Executor: executor, Attempt: attempt}
	if err := documents.validate(); err != nil {
		return EvidenceDocuments{}, "", err
	}
	if reader.Outcome().AttemptID != attempt.ID {
		return EvidenceDocuments{}, "", fmt.Errorf("Outcome attemptId %q disagrees with Attempt %q", reader.Outcome().AttemptID, attempt.ID)
	}

	switch subject.Provider.Lane {
	case ProviderLaneFixture:
		return documents, LaneFixture, nil
	case ProviderLaneLive:
		return documents, LaneLive, nil
	default:
		return EvidenceDocuments{}, "", fmt.Errorf("unsupported frozen Subject lane %q", subject.Provider.Lane)
	}
}
