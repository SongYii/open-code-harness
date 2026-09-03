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
// every scoreable Attempt (design §14). Attempt identity binds the
// Scenario/Subject/Executor documents by digest; collection stages them,
// the Attempt, and the frozen EvalSet into the manifest, plus the
// JudgeConfig on a live Attempt.
//
// EvalSet and JudgeConfig are what make a live quality Score provable
// offline: without the set staged beside the Attempt there is nothing to
// prove which judge configuration the run was entitled to use, and
// without the configuration staged there is nothing to prove which one it
// actually used.
type EvidenceDocuments struct {
	Scenario Scenario
	Subject  Subject
	Executor Executor
	Attempt  Attempt

	// EvalSet is the frozen set this Attempt was expanded from. Every new
	// Attempt stages it, in both lanes.
	EvalSet EvalSet

	// JudgeConfig is the frozen judge configuration EvalSet.JudgeConfigDigest
	// names. It is required on a live Attempt and forbidden on a fixture
	// one, exactly mirroring the EvalSet's own lane rule.
	JudgeConfig *JudgeConfig
}

// validateIdentity checks the four-document identity chain every Attempt
// has always carried. It is deliberately separate from validateBinding so
// an Attempt collected before EvalSet/JudgeConfig evidence existed still
// regrades deterministically.
func (documents EvidenceDocuments) validateIdentity() error {
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

// validateBinding checks that the frozen EvalSet really is the set this
// Attempt came from, and that the JudgeConfig really is the one that set
// authorized. Every check here is what a later reader needs in order to
// reconstruct a live Score's judge identity from evidence alone, so each
// one is enforced both when staging and when reading back.
func (documents EvidenceDocuments) validateBinding() error {
	if err := documents.EvalSet.Validate(); err != nil {
		return fmt.Errorf("eval set: %w", err)
	}
	if documents.Attempt.EvalSetID != documents.EvalSet.ID {
		return fmt.Errorf("attempt evalSetId %q disagrees with frozen eval set %q", documents.Attempt.EvalSetID, documents.EvalSet.ID)
	}

	// The Attempt's own cell must appear in the set at exactly the digests
	// the Attempt froze, so a set cannot be swapped for an unrelated one
	// that happens to share an ID.
	scenarioFound := false
	for _, ref := range documents.EvalSet.Scenarios {
		if ref.ID == documents.Attempt.ScenarioID {
			if ref.Digest != documents.Attempt.ScenarioDigest {
				return fmt.Errorf("eval set references scenario %q at digest %q, but the attempt froze %q",
					ref.ID, ref.Digest, documents.Attempt.ScenarioDigest)
			}
			scenarioFound = true
		}
	}
	if !scenarioFound {
		return fmt.Errorf("frozen eval set %q does not reference scenario %q", documents.EvalSet.ID, documents.Attempt.ScenarioID)
	}
	subjectFound := false
	for _, ref := range documents.EvalSet.Subjects {
		if ref.ID == documents.Attempt.SubjectID {
			if ref.Digest != documents.Attempt.SubjectDigest {
				return fmt.Errorf("eval set references subject %q at digest %q, but the attempt froze %q",
					ref.ID, ref.Digest, documents.Attempt.SubjectDigest)
			}
			subjectFound = true
		}
	}
	if !subjectFound {
		return fmt.Errorf("frozen eval set %q does not reference subject %q", documents.EvalSet.ID, documents.Attempt.SubjectID)
	}
	executorFound := false
	for _, ref := range documents.EvalSet.Executors {
		if ref.ID == documents.Attempt.ExecutorID {
			if ref.Digest != documents.Attempt.ExecutorDigest {
				return fmt.Errorf("eval set references executor %q at digest %q, but the attempt froze %q",
					ref.ID, ref.Digest, documents.Attempt.ExecutorDigest)
			}
			executorFound = true
		}
	}
	if !executorFound {
		return fmt.Errorf("frozen eval set %q does not reference executor %q", documents.EvalSet.ID, documents.Attempt.ExecutorID)
	}

	// Lane parity: ExpandAttempts enforces this before a run starts, but a
	// reader that opens an Attempt months later has no expansion to rely
	// on, so the evidence must carry the proof itself.
	switch documents.EvalSet.Lane {
	case LaneFixture:
		if documents.Subject.Provider.Lane != ProviderLaneFixture {
			return fmt.Errorf("%q lane eval set froze a %q lane subject", LaneFixture, documents.Subject.Provider.Lane)
		}
		if documents.JudgeConfig != nil {
			return fmt.Errorf("a %q lane attempt must not carry a judge configuration", LaneFixture)
		}
	case LaneLive:
		if documents.Subject.Provider.Lane != ProviderLaneLive {
			return fmt.Errorf("%q lane eval set froze a %q lane subject", LaneLive, documents.Subject.Provider.Lane)
		}
		if documents.JudgeConfig == nil {
			return fmt.Errorf("a %q lane attempt requires the judge configuration its eval set names", LaneLive)
		}
		digest, err := JudgeConfigDigest(*documents.JudgeConfig)
		if err != nil {
			return err
		}
		if digest != documents.EvalSet.JudgeConfigDigest {
			return fmt.Errorf("judge config digest %q disagrees with the frozen eval set's judgeConfigDigest %q",
				digest, documents.EvalSet.JudgeConfigDigest)
		}
	default:
		return fmt.Errorf("unsupported frozen eval set lane %q", documents.EvalSet.Lane)
	}
	return nil
}

func stageEvidenceDocuments(directories AttemptRootDirectories, documents EvidenceDocuments, budget *collectionBudget) error {
	if err := documents.validateIdentity(); err != nil {
		return fmt.Errorf("validate frozen evidence documents: %w", err)
	}
	if err := documents.validateBinding(); err != nil {
		return fmt.Errorf("validate frozen evidence binding: %w", err)
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
		{path: "eval-set.json", role: "eval_set"},
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
	values[4].data, err = json.Marshal(documents.EvalSet)
	if err != nil {
		return fmt.Errorf("encode frozen eval set: %w", err)
	}
	// validateBinding above already refused a JudgeConfig on a fixture
	// Attempt and a missing one on a live Attempt, so presence here is
	// exactly the lane rule.
	if documents.JudgeConfig != nil {
		configBytes, err := json.Marshal(*documents.JudgeConfig)
		if err != nil {
			return fmt.Errorf("encode frozen judge config: %w", err)
		}
		values = append(values, struct {
			path string
			role string
			data []byte
		}{path: "judge-config.json", role: "judge_config", data: configBytes})
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
	if err := documents.validateIdentity(); err != nil {
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

// readJudgeEvidenceDocuments is the live judge path's own reader: it
// requires everything readEvidenceDocuments requires, plus the frozen
// EvalSet and JudgeConfig evidence a live Attempt stages, and it refuses
// anything that is not a live Attempt.
//
// It is deliberately a separate entry point rather than a stricter mode of
// readEvidenceDocuments: an Attempt collected before those roles existed
// must keep regrading deterministically, so the legacy reader must never
// learn to demand them. What such an Attempt loses is only the ability to
// be live-judged, which is the honest outcome — nothing in its evidence
// could prove which judge configuration it was entitled to use.
func readJudgeEvidenceDocuments(reader *ArtifactReader, publishedAttempt Attempt) (EvidenceDocuments, EvalLane, error) {
	documents, lane, err := readEvidenceDocuments(reader, publishedAttempt)
	if err != nil {
		return EvidenceDocuments{}, "", err
	}
	if lane != LaneLive {
		return EvidenceDocuments{}, "", fmt.Errorf("judge evidence requires a %q lane Attempt, not %q", LaneLive, lane)
	}

	setBytes, err := readSingleCollectedEntry(reader, "eval_set")
	if err != nil {
		return EvidenceDocuments{}, "", err
	}
	set, err := DecodeEvalSet(setBytes)
	if err != nil {
		return EvidenceDocuments{}, "", err
	}
	configBytes, err := readSingleCollectedEntry(reader, "judge_config")
	if err != nil {
		return EvidenceDocuments{}, "", err
	}
	config, err := DecodeJudgeConfig(configBytes)
	if err != nil {
		return EvidenceDocuments{}, "", err
	}

	documents.EvalSet = set
	documents.JudgeConfig = &config
	if err := documents.validateBinding(); err != nil {
		return EvidenceDocuments{}, "", err
	}
	return documents, LaneLive, nil
}

// readSingleCollectedEntry returns the exact bytes of the one collected
// manifest entry carrying role. More than one, or any state other than
// collected, is a refusal rather than a choice: frozen identity evidence
// that appears twice has no single answer to "which one did this Attempt
// actually use".
func readSingleCollectedEntry(reader *ArtifactReader, role string) ([]byte, error) {
	entries := reader.Entries(role)
	if len(entries) != 1 || entries[0].State != EntryCollected {
		return nil, fmt.Errorf("frozen %s evidence must contain exactly one collected entry", role)
	}
	return reader.ReadEntry(entries[0].Path)
}
