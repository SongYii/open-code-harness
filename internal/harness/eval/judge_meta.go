package eval

import (
	"context"
	"fmt"
	"reflect"
	"sort"

	"github.com/SongYii/open-code-harness/internal/harness/redact"
)

const (
	SchemaJudgeMetaSet    = "och.eval.judge-meta-set"
	SchemaJudgeMetaReport = "och.eval.judge-meta-report"

	maxJudgeMetaCases       = 256
	maxJudgeMetaRepetitions = 20
	maxJudgeMetaLabelBytes  = 4096
)

// JudgeMetaLabel is trusted, human-reviewed ground truth. Rationale is review
// evidence for the label and is deliberately never rendered into a judge
// request.
type JudgeMetaLabel struct {
	Verdict   ScoreVerdict `json:"verdict"`
	Rationale string       `json:"rationale"`
}

// JudgeMetaEvidence is one synthetic manifest-like record shown to the judge.
type JudgeMetaEvidence struct {
	Path string `json:"path"`
	Role string `json:"role"`
	Text string `json:"text"`
}

type JudgeMetaCase struct {
	ID       string              `json:"id"`
	Label    JudgeMetaLabel      `json:"label"`
	Tags     []string            `json:"tags,omitempty"`
	Evidence []JudgeMetaEvidence `json:"evidence"`
}

// JudgeMetaSet is a frozen labelled corpus for one exact JudgeConfig.
type JudgeMetaSet struct {
	FormatVersion     int             `json:"formatVersion"`
	Schema            string          `json:"schema"`
	ID                string          `json:"id"`
	Version           string          `json:"version"`
	JudgeConfigDigest Digest          `json:"judgeConfigDigest"`
	RepetitionCount   int             `json:"repetitionCount"`
	Cases             []JudgeMetaCase `json:"cases"`
}

func DecodeJudgeMetaSet(data []byte) (JudgeMetaSet, error) {
	var set JudgeMetaSet
	if err := decodeStrict(data, &set); err != nil {
		return JudgeMetaSet{}, fmt.Errorf("eval: judge meta set: %w", err)
	}
	if set.Schema != SchemaJudgeMetaSet {
		return JudgeMetaSet{}, fmt.Errorf("eval: judge meta set: %w: %q", errUnsupportedSchema, set.Schema)
	}
	if set.FormatVersion != FormatVersion {
		return JudgeMetaSet{}, fmt.Errorf("eval: judge meta set: %w: %d", errUnsupportedFormatVersion, set.FormatVersion)
	}
	if err := set.Validate(); err != nil {
		return JudgeMetaSet{}, err
	}
	return set, nil
}

func (set JudgeMetaSet) Validate() error {
	if set.FormatVersion != FormatVersion || set.Schema != SchemaJudgeMetaSet {
		return fmt.Errorf("%w: invalid judge meta set format/schema", errInvalidDocument)
	}
	if !hasText(set.ID) || !hasText(set.Version) {
		return fmt.Errorf("%w: judge meta set id and version are required", errInvalidDocument)
	}
	if !digestStringPattern.MatchString(string(set.JudgeConfigDigest)) {
		return fmt.Errorf("%w: judgeConfigDigest must be sha256:<64 lowercase hex>", errInvalidDocument)
	}
	if set.RepetitionCount < 1 || set.RepetitionCount > maxJudgeMetaRepetitions {
		return fmt.Errorf("%w: repetitionCount must be between 1 and %d", errInvalidDocument, maxJudgeMetaRepetitions)
	}
	if len(set.Cases) == 0 || len(set.Cases) > maxJudgeMetaCases {
		return fmt.Errorf("%w: cases must contain between 1 and %d entries", errInvalidDocument, maxJudgeMetaCases)
	}
	seenCases := make(map[string]bool, len(set.Cases))
	for index, metaCase := range set.Cases {
		if err := metaCase.validate(index); err != nil {
			return err
		}
		if seenCases[metaCase.ID] {
			return fmt.Errorf("%w: repeated judge meta case id %q", errInvalidDocument, metaCase.ID)
		}
		seenCases[metaCase.ID] = true
	}
	return nil
}

func (metaCase JudgeMetaCase) validate(index int) error {
	if !hasText(metaCase.ID) {
		return fmt.Errorf("%w: cases %d: id is required", errInvalidDocument, index)
	}
	switch metaCase.Label.Verdict {
	case ScorePass, ScoreFail, ScoreIndeterminate:
	default:
		return fmt.Errorf("%w: cases %d: invalid label verdict %q", errInvalidDocument, index, metaCase.Label.Verdict)
	}
	if !hasText(metaCase.Label.Rationale) || len(metaCase.Label.Rationale) > maxJudgeMetaLabelBytes {
		return fmt.Errorf("%w: cases %d: label rationale must contain at most %d bytes", errInvalidDocument, index, maxJudgeMetaLabelBytes)
	}
	seenTags := make(map[string]bool, len(metaCase.Tags))
	for _, tag := range metaCase.Tags {
		if !hasText(tag) || seenTags[tag] {
			return fmt.Errorf("%w: cases %d: tags must be non-empty and unique", errInvalidDocument, index)
		}
		seenTags[tag] = true
	}
	if len(metaCase.Evidence) == 0 {
		return fmt.Errorf("%w: cases %d: evidence is required", errInvalidDocument, index)
	}
	seenPaths := make(map[string]bool, len(metaCase.Evidence))
	total := 0
	for evidenceIndex, evidence := range metaCase.Evidence {
		if err := validateContainedRelativePath(evidence.Path); err != nil {
			return fmt.Errorf("%w: cases %d evidence %d path: %v", errInvalidDocument, index, evidenceIndex, err)
		}
		if seenPaths[evidence.Path] {
			return fmt.Errorf("%w: cases %d: repeated evidence path %q", errInvalidDocument, index, evidence.Path)
		}
		seenPaths[evidence.Path] = true
		if !hasText(evidence.Role) {
			return fmt.Errorf("%w: cases %d evidence %d: role is required", errInvalidDocument, index, evidenceIndex)
		}
		if len(evidence.Text) > maxJudgeEvidenceEntryBytes {
			return fmt.Errorf("%w: cases %d evidence %d exceeds %d-byte entry limit", errInvalidDocument, index, evidenceIndex, maxJudgeEvidenceEntryBytes)
		}
		total += len(evidence.Text)
	}
	if total > maxJudgeEvidenceBundleBytes {
		return fmt.Errorf("%w: cases %d evidence exceeds %d-byte bundle limit", errInvalidDocument, index, maxJudgeEvidenceBundleBytes)
	}
	return nil
}

func JudgeMetaSetDigest(set JudgeMetaSet) (Digest, error) {
	if err := set.Validate(); err != nil {
		return "", err
	}
	return canonicalDigest(set)
}

func VerifyJudgeMetaSetBinding(set JudgeMetaSet, config JudgeConfig) error {
	if err := set.Validate(); err != nil {
		return err
	}
	digest, err := JudgeConfigDigest(config)
	if err != nil {
		return err
	}
	if digest != set.JudgeConfigDigest {
		return fmt.Errorf("%w: judge meta set config digest %q disagrees with %q", errInvalidDocument, set.JudgeConfigDigest, digest)
	}
	declaredRoles := make(map[string]bool)
	for _, criterion := range config.Criteria {
		for _, role := range criterion.EvidenceRoles {
			declaredRoles[role] = true
		}
	}
	for _, metaCase := range set.Cases {
		present := make(map[string]bool)
		for _, evidence := range metaCase.Evidence {
			if !declaredRoles[evidence.Role] {
				return fmt.Errorf("%w: judge meta case %q uses undeclared evidence role %q", errInvalidDocument, metaCase.ID, evidence.Role)
			}
			present[evidence.Role] = true
		}
		for role := range declaredRoles {
			if !present[role] {
				return fmt.Errorf("%w: judge meta case %q has no evidence for declared role %q", errInvalidDocument, metaCase.ID, role)
			}
		}
	}
	return nil
}

func (set JudgeMetaSet) CallCount() int { return len(set.Cases) * set.RepetitionCount }

type JudgeMetaObservation struct {
	CaseID                string            `json:"caseId"`
	RepetitionIndex       int               `json:"repetitionIndex"`
	ExpectedVerdict       ScoreVerdict      `json:"expectedVerdict"`
	ObservedVerdict       ScoreVerdict      `json:"observedVerdict"`
	NumericScore          *float64          `json:"numericScore,omitempty"`
	Criteria              []CriterionResult `json:"criteria"`
	EvidenceReferences    []string          `json:"evidenceReferences,omitempty"`
	MissingEvidence       []string          `json:"missingEvidence,omitempty"`
	ContradictoryEvidence []string          `json:"contradictoryEvidence,omitempty"`
	Rationale             string            `json:"rationale,omitempty"`
	ScorerUsage           ScorerUsage       `json:"scorerUsage"`
}

type JudgeMetaConfusionCell struct {
	Expected ScoreVerdict `json:"expected"`
	Observed ScoreVerdict `json:"observed"`
	Count    int          `json:"count"`
}

type JudgeMetaSummary struct {
	Observations             int `json:"observations"`
	ExactMatches             int `json:"exactMatches"`
	UnsafePasses             int `json:"unsafePasses"`
	FalseFails               int `json:"falseFails"`
	UnexpectedIndeterminates int `json:"unexpectedIndeterminates"`
	Overclaims               int `json:"overclaims"`
}

type JudgeMetaReport struct {
	FormatVersion      int                      `json:"formatVersion"`
	Schema             string                   `json:"schema"`
	SetID              string                   `json:"setId"`
	SetVersion         string                   `json:"setVersion"`
	SetDigest          Digest                   `json:"setDigest"`
	JudgeConfigID      string                   `json:"judgeConfigId"`
	JudgeConfigVersion string                   `json:"judgeConfigVersion"`
	JudgeConfigDigest  Digest                   `json:"judgeConfigDigest"`
	ModelID            string                   `json:"modelId"`
	PlannedCalls       int                      `json:"plannedCalls"`
	CompletedCalls     int                      `json:"completedCalls"`
	Complete           bool                     `json:"complete"`
	StopReason         string                   `json:"stopReason,omitempty"`
	Observations       []JudgeMetaObservation   `json:"observations"`
	Confusion          []JudgeMetaConfusionCell `json:"confusion"`
	Summary            JudgeMetaSummary         `json:"summary"`
}

// RunJudgeMetaSet measures the production judge path against human-reviewed
// labels. It does no hidden retry: cases × repetitions is exactly the number
// of caller invocations unless context cancellation aborts the run.
func RunJudgeMetaSet(ctx context.Context, set JudgeMetaSet, config JudgeConfig, caller JudgeCaller, priceTable *PriceTable) (JudgeMetaReport, error) {
	if ctx == nil {
		return JudgeMetaReport{}, fmt.Errorf("eval: run judge meta set: context is required")
	}
	if caller == nil {
		return JudgeMetaReport{}, fmt.Errorf("eval: run judge meta set: caller is required")
	}
	if err := VerifyJudgeMetaSetBinding(set, config); err != nil {
		return JudgeMetaReport{}, fmt.Errorf("eval: run judge meta set: %w", err)
	}
	if err := verifyJudgeMetaPriceTable(config, priceTable); err != nil {
		return JudgeMetaReport{}, fmt.Errorf("eval: run judge meta set: %w", err)
	}
	setDigest, err := JudgeMetaSetDigest(set)
	if err != nil {
		return JudgeMetaReport{}, err
	}
	configDigest, err := JudgeConfigDigest(config)
	if err != nil {
		return JudgeMetaReport{}, err
	}
	report := JudgeMetaReport{
		FormatVersion: FormatVersion, Schema: SchemaJudgeMetaReport,
		SetID: set.ID, SetVersion: set.Version, SetDigest: setDigest,
		JudgeConfigID: config.ID, JudgeConfigVersion: config.Version,
		JudgeConfigDigest: configDigest, ModelID: config.Provider.ModelID,
		PlannedCalls: set.CallCount(),
	}
	for _, metaCase := range set.Cases {
		bundle, err := buildJudgeMetaEvidenceBundle(config, metaCase)
		if err != nil {
			return JudgeMetaReport{}, fmt.Errorf("eval: run judge meta set: case %q: %w", metaCase.ID, err)
		}
		for repetition := 0; repetition < set.RepetitionCount; repetition++ {
			if err := ctx.Err(); err != nil {
				report.StopReason = boundedString(err.Error(), maxJudgeRationaleBytes)
				return validateFinishedJudgeMetaReport(report)
			}
			outcome, err := runJudgeWithBundle(ctx, bundle, config, caller)
			if err != nil {
				return JudgeMetaReport{}, err
			}
			usage := outcome.Usage
			usage.CostStatus, usage.CostMicrounits, usage.CostCurrency = ResolveScorerCost(
				priceTable, config.Provider.ModelID, uint64(max(usage.InputTokens, 0)), uint64(max(usage.OutputTokens, 0)), 0)
			report.Observations = append(report.Observations, JudgeMetaObservation{
				CaseID: metaCase.ID, RepetitionIndex: repetition,
				ExpectedVerdict: metaCase.Label.Verdict, ObservedVerdict: outcome.Verdict,
				NumericScore: outcome.NumericScore, Criteria: outcome.Criteria, EvidenceReferences: outcome.EvidenceReferences,
				MissingEvidence: outcome.MissingEvidence, ContradictoryEvidence: outcome.ContradictoryEvidence,
				Rationale: outcome.Rationale, ScorerUsage: usage,
			})
		}
	}
	report.Complete = true
	return validateFinishedJudgeMetaReport(report)
}

func finishJudgeMetaReport(report JudgeMetaReport) JudgeMetaReport {
	report.CompletedCalls = len(report.Observations)
	report.Confusion, report.Summary = summarizeJudgeMeta(report.Observations)
	return report
}

func validateFinishedJudgeMetaReport(report JudgeMetaReport) (JudgeMetaReport, error) {
	report = finishJudgeMetaReport(report)
	if err := report.Validate(); err != nil {
		return JudgeMetaReport{}, fmt.Errorf("eval: run judge meta set: %w", err)
	}
	return report, nil
}

func buildJudgeMetaEvidenceBundle(config JudgeConfig, metaCase JudgeMetaCase) (judgeEvidenceBundle, error) {
	ordered := append([]JudgeMetaEvidence(nil), metaCase.Evidence...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Path < ordered[j].Path })
	entries := make([]judgeEvidenceEntry, 0, len(ordered))
	paths := make([]string, 0, len(ordered))
	pathRoles := make(map[string][]string, len(ordered))
	for _, evidence := range ordered {
		redacted := redact.Text(evidence.Text)
		text := boundedString(redacted, maxJudgeEvidenceEntryBytes)
		entries = append(entries, judgeEvidenceEntry{
			Path: evidence.Path, OriginalBytes: len(evidence.Text), Text: text, Truncated: len(text) != len(redacted),
		})
		paths = append(paths, evidence.Path)
		pathRoles[evidence.Path] = []string{evidence.Role}
	}
	text, err := renderJudgeEvidenceBundle(config, entries)
	if err != nil {
		return judgeEvidenceBundle{}, err
	}
	return judgeEvidenceBundle{Text: text, AvailablePaths: paths, PathRoles: pathRoles}, nil
}

func summarizeJudgeMeta(observations []JudgeMetaObservation) ([]JudgeMetaConfusionCell, JudgeMetaSummary) {
	verdicts := []ScoreVerdict{ScorePass, ScoreFail, ScoreIndeterminate}
	counts := make(map[[2]ScoreVerdict]int, 9)
	summary := JudgeMetaSummary{Observations: len(observations)}
	for _, observation := range observations {
		counts[[2]ScoreVerdict{observation.ExpectedVerdict, observation.ObservedVerdict}]++
		if observation.ExpectedVerdict == observation.ObservedVerdict {
			summary.ExactMatches++
		}
		if observation.ExpectedVerdict != ScorePass && observation.ObservedVerdict == ScorePass {
			summary.UnsafePasses++
		}
		if observation.ExpectedVerdict == ScorePass && observation.ObservedVerdict == ScoreFail {
			summary.FalseFails++
		}
		if observation.ExpectedVerdict != ScoreIndeterminate && observation.ObservedVerdict == ScoreIndeterminate {
			summary.UnexpectedIndeterminates++
		}
		if observation.ExpectedVerdict == ScoreIndeterminate && observation.ObservedVerdict != ScoreIndeterminate {
			summary.Overclaims++
		}
	}
	cells := make([]JudgeMetaConfusionCell, 0, 9)
	for _, expected := range verdicts {
		for _, observed := range verdicts {
			cells = append(cells, JudgeMetaConfusionCell{Expected: expected, Observed: observed, Count: counts[[2]ScoreVerdict{expected, observed}]})
		}
	}
	return cells, summary
}

func DecodeJudgeMetaReport(data []byte) (JudgeMetaReport, error) {
	var report JudgeMetaReport
	if err := decodeStrict(data, &report); err != nil {
		return JudgeMetaReport{}, fmt.Errorf("eval: judge meta report: %w", err)
	}
	if err := report.Validate(); err != nil {
		return JudgeMetaReport{}, err
	}
	return report, nil
}

func (report JudgeMetaReport) Validate() error {
	if report.FormatVersion != FormatVersion || report.Schema != SchemaJudgeMetaReport {
		return fmt.Errorf("%w: invalid judge meta report format/schema", errInvalidDocument)
	}
	if !hasText(report.SetID) || !hasText(report.SetVersion) || !hasText(report.JudgeConfigID) ||
		!hasText(report.JudgeConfigVersion) || !hasText(report.ModelID) {
		return fmt.Errorf("%w: judge meta report identity fields are required", errInvalidDocument)
	}
	if !digestStringPattern.MatchString(string(report.SetDigest)) || !digestStringPattern.MatchString(string(report.JudgeConfigDigest)) {
		return fmt.Errorf("%w: judge meta report digests are invalid", errInvalidDocument)
	}
	if report.PlannedCalls < 1 || report.CompletedCalls != len(report.Observations) || report.CompletedCalls > report.PlannedCalls {
		return fmt.Errorf("%w: judge meta report call counts are inconsistent", errInvalidDocument)
	}
	if report.Complete != (report.CompletedCalls == report.PlannedCalls) {
		return fmt.Errorf("%w: judge meta report completion disagrees with call counts", errInvalidDocument)
	}
	if report.Complete && report.StopReason != "" {
		return fmt.Errorf("%w: complete judge meta report carries a stop reason", errInvalidDocument)
	}
	if !report.Complete && !hasText(report.StopReason) {
		return fmt.Errorf("%w: incomplete judge meta report requires a stop reason", errInvalidDocument)
	}
	if len(report.StopReason) > maxJudgeRationaleBytes {
		return fmt.Errorf("%w: judge meta report stop reason is too long", errInvalidDocument)
	}
	for index, observation := range report.Observations {
		if !hasText(observation.CaseID) || observation.RepetitionIndex < 0 {
			return fmt.Errorf("%w: judge meta observation %d identity is invalid", errInvalidDocument, index)
		}
		for _, verdict := range []ScoreVerdict{observation.ExpectedVerdict, observation.ObservedVerdict} {
			switch verdict {
			case ScorePass, ScoreFail, ScoreIndeterminate:
			default:
				return fmt.Errorf("%w: judge meta observation %d verdict %q is invalid", errInvalidDocument, index, verdict)
			}
		}
		if err := validateOptionalFiniteScore(observation.NumericScore); err != nil {
			return err
		}
		if len(observation.Criteria) == 0 && observation.ObservedVerdict != ScoreIndeterminate {
			return fmt.Errorf("%w: judge meta observation %d has no criterion results", errInvalidDocument, index)
		}
		seenCriteria := make(map[string]bool, len(observation.Criteria))
		for criterionIndex, criterion := range observation.Criteria {
			if err := criterion.validate(criterionIndex); err != nil {
				return err
			}
			if seenCriteria[criterion.ID] {
				return fmt.Errorf("%w: judge meta observation %d repeats criterion %q", errInvalidDocument, index, criterion.ID)
			}
			seenCriteria[criterion.ID] = true
		}
		if len(observation.Rationale) > maxJudgeRationaleBytes {
			return fmt.Errorf("%w: judge meta observation %d rationale is too long", errInvalidDocument, index)
		}
		if err := observation.ScorerUsage.validate(); err != nil {
			return err
		}
		for name, values := range map[string][]string{
			"evidenceReferences":    observation.EvidenceReferences,
			"missingEvidence":       observation.MissingEvidence,
			"contradictoryEvidence": observation.ContradictoryEvidence,
		} {
			if err := requireNonEmptyEntries(name, values); err != nil {
				return err
			}
		}
	}
	wantConfusion, wantSummary := summarizeJudgeMeta(report.Observations)
	if !reflect.DeepEqual(report.Confusion, wantConfusion) || report.Summary != wantSummary {
		return fmt.Errorf("%w: judge meta report aggregates disagree with observations", errInvalidDocument)
	}
	return nil
}

// VerifyJudgeMetaReportBinding proves that report is an ordered complete or
// prefix-partial observation of this exact labelled set under this exact judge
// configuration. Report.Validate alone can prove only internal arithmetic.
func VerifyJudgeMetaReportBinding(report JudgeMetaReport, set JudgeMetaSet, config JudgeConfig) error {
	if err := report.Validate(); err != nil {
		return err
	}
	if err := VerifyJudgeMetaSetBinding(set, config); err != nil {
		return err
	}
	setDigest, err := JudgeMetaSetDigest(set)
	if err != nil {
		return err
	}
	configDigest, err := JudgeConfigDigest(config)
	if err != nil {
		return err
	}
	if report.SetID != set.ID || report.SetVersion != set.Version || report.SetDigest != setDigest ||
		report.JudgeConfigID != config.ID || report.JudgeConfigVersion != config.Version ||
		report.JudgeConfigDigest != configDigest || report.ModelID != config.Provider.ModelID {
		return fmt.Errorf("%w: judge meta report identity disagrees with its set or judge config", errInvalidDocument)
	}
	if report.PlannedCalls != set.CallCount() {
		return fmt.Errorf("%w: judge meta report plannedCalls disagrees with its set", errInvalidDocument)
	}
	expected := make([]struct {
		caseID     string
		repetition int
		verdict    ScoreVerdict
	}, 0, set.CallCount())
	for _, metaCase := range set.Cases {
		for repetition := 0; repetition < set.RepetitionCount; repetition++ {
			expected = append(expected, struct {
				caseID     string
				repetition int
				verdict    ScoreVerdict
			}{metaCase.ID, repetition, metaCase.Label.Verdict})
		}
	}
	for index, observation := range report.Observations {
		want := expected[index]
		if observation.CaseID != want.caseID || observation.RepetitionIndex != want.repetition || observation.ExpectedVerdict != want.verdict {
			return fmt.Errorf("%w: judge meta observation %d disagrees with labelled set order", errInvalidDocument, index)
		}
		if len(observation.Criteria) > 0 {
			gotCriteria := make(map[string]bool, len(observation.Criteria))
			for _, criterion := range observation.Criteria {
				gotCriteria[criterion.ID] = true
			}
			if len(gotCriteria) != len(config.Criteria) {
				return fmt.Errorf("%w: judge meta observation %d criteria disagree with judge config", errInvalidDocument, index)
			}
			for _, criterion := range config.Criteria {
				if !gotCriteria[criterion.ID] {
					return fmt.Errorf("%w: judge meta observation %d criteria disagree with judge config", errInvalidDocument, index)
				}
			}
		}
	}
	return nil
}

func verifyJudgeMetaPriceTable(config JudgeConfig, table *PriceTable) error {
	if config.PriceTableDigest == "" {
		if table != nil {
			return fmt.Errorf("%w: price table supplied but judge config names no priceTableDigest", errInvalidDocument)
		}
		return nil
	}
	if table == nil {
		return fmt.Errorf("%w: judge config names priceTableDigest but no price table was supplied", errInvalidDocument)
	}
	digest, err := PriceTableDigest(*table)
	if err != nil {
		return err
	}
	if digest != config.PriceTableDigest {
		return fmt.Errorf("%w: price table digest %q disagrees with judge config %q", errInvalidDocument, digest, config.PriceTableDigest)
	}
	return nil
}
