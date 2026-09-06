package eval

import (
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
)

// validVariancePolicy is the baseline every case below mutates one field of,
// so a failure names exactly one cause.
//
// Its numbers are deliberately arbitrary. This repository has never run a
// judge against a live model, so no calibrated value exists to use here, and
// the document says so about itself — which is the point of the Calibration
// field rather than a comment.
func validVariancePolicy() VariancePolicy {
	return VariancePolicy{
		FormatVersion: FormatVersion,
		Schema:        SchemaVariancePolicy,
		ID:            "provisional-v1",
		Version:       "v1",
		Calibration:   CalibrationUncalibrated,

		MaxNumericSpread:        0.20,
		MinVerdictStability:     0.80,
		MinEvaluableRepetitions: 3,
	}
}

func marshalPolicy(t *testing.T, policy VariancePolicy) []byte {
	t.Helper()
	data, err := json.Marshal(policy)
	if err != nil {
		t.Fatalf("marshal policy: %v", err)
	}
	return data
}

func TestDecodeVariancePolicyRoundTripAndDigest(t *testing.T) {
	want := validVariancePolicy()
	got, err := DecodeVariancePolicy(marshalPolicy(t, want))
	if err != nil {
		t.Fatalf("DecodeVariancePolicy: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip changed the document:\n got %+v\nwant %+v", got, want)
	}

	first, err := VariancePolicyDigest(want)
	if err != nil {
		t.Fatalf("VariancePolicyDigest: %v", err)
	}
	second, err := VariancePolicyDigest(got)
	if err != nil {
		t.Fatalf("VariancePolicyDigest: %v", err)
	}
	if first != second {
		t.Fatalf("digest is not stable across a round trip: %q != %q", first, second)
	}
}

// TestVariancePolicyRequiresBothLimits is the design's "no default
// thresholds, ever" rule. A shipped default would be a guess wearing the
// authority of a specification, since no live run has ever produced a real
// one here.
func TestVariancePolicyRequiresBothLimits(t *testing.T) {
	for name, mutate := range map[string]func(*VariancePolicy){
		"missing numeric spread":    func(p *VariancePolicy) { p.MaxNumericSpread = 0 },
		"missing verdict stability": func(p *VariancePolicy) { p.MinVerdictStability = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			policy := validVariancePolicy()
			mutate(&policy)
			if err := policy.Validate(); !errors.Is(err, errInvalidDocument) {
				t.Fatalf("Validate() = %v, want a refusal rather than a default", err)
			}
		})
	}
}

// TestVariancePolicyRequiresAnExplicitCalibrationState closes the hazard the
// accepted ordering carries: shipping the mechanism before any calibrated
// number exists means a provisional value can be mistaken for a final one.
// Making the state a mandatory field rather than an absence is what stops
// that being a matter of anyone remembering.
func TestVariancePolicyRequiresAnExplicitCalibrationState(t *testing.T) {
	policy := validVariancePolicy()
	policy.Calibration = ""
	if err := policy.Validate(); !errors.Is(err, errInvalidDocument) {
		t.Fatalf("Validate() = %v, want an unstated calibration to be refused", err)
	}

	policy.Calibration = "probably-fine"
	if err := policy.Validate(); !errors.Is(err, errInvalidDocument) {
		t.Fatalf("Validate() = %v, want an unknown calibration state to be refused", err)
	}

	// Both declared states are legal. A calibrated one additionally has to
	// cite its run, which TestCalibratedPolicyMustCiteTheRunThatProducedIt
	// covers on its own; supplying it here keeps this test about the state
	// itself rather than about that second rule.
	for _, state := range []Calibration{CalibrationUncalibrated, CalibrationCalibrated} {
		policy := validVariancePolicy()
		policy.Calibration = state
		if state == CalibrationCalibrated {
			policy.CalibratedFrom = "eval/artifacts/run"
		}
		if err := policy.Validate(); err != nil {
			t.Fatalf("Validate() rejected the declared state %q: %v", state, err)
		}
	}
}

// TestCalibratedPolicyMustCiteTheRunThatProducedIt: a calibrated claim is
// only meaningful if the evidence behind it can be found. An uncalibrated
// policy has nothing to cite and must not be forced to invent one.
func TestCalibratedPolicyMustCiteTheRunThatProducedIt(t *testing.T) {
	policy := validVariancePolicy()
	policy.Calibration = CalibrationCalibrated
	if err := policy.Validate(); !errors.Is(err, errInvalidDocument) {
		t.Fatalf("Validate() = %v, want a calibrated policy with no cited evidence to be refused", err)
	}

	policy.CalibratedFrom = "eval/artifacts/2026-09-05-live-run"
	if err := policy.Validate(); err != nil {
		t.Fatalf("Validate() rejected a calibrated policy that cites its run: %v", err)
	}

	uncalibrated := validVariancePolicy()
	uncalibrated.CalibratedFrom = "somewhere"
	if err := uncalibrated.Validate(); !errors.Is(err, errInvalidDocument) {
		t.Fatalf("Validate() = %v, want an uncalibrated policy citing a run to be refused", err)
	}
}

func TestVariancePolicyRefusesOutOfRangeLimits(t *testing.T) {
	for name, mutate := range map[string]func(*VariancePolicy){
		"negative spread":      func(p *VariancePolicy) { p.MaxNumericSpread = -0.1 },
		"spread above one":     func(p *VariancePolicy) { p.MaxNumericSpread = 1.5 },
		"stability above one":  func(p *VariancePolicy) { p.MinVerdictStability = 1.5 },
		"stability below zero": func(p *VariancePolicy) { p.MinVerdictStability = -0.1 },
		"spread not a number":  func(p *VariancePolicy) { p.MaxNumericSpread = math.NaN() },
		"stability infinite":   func(p *VariancePolicy) { p.MinVerdictStability = math.Inf(1) },
		"negative repetitions": func(p *VariancePolicy) { p.MinEvaluableRepetitions = -1 },
		"zero min repetitions": func(p *VariancePolicy) { p.MinEvaluableRepetitions = 0 },
		"one min repetition":   func(p *VariancePolicy) { p.MinEvaluableRepetitions = 1 },
	} {
		t.Run(name, func(t *testing.T) {
			policy := validVariancePolicy()
			mutate(&policy)
			if err := policy.Validate(); !errors.Is(err, errInvalidDocument) {
				t.Fatalf("Validate() = %v, want a refusal", err)
			}
		})
	}
}

// TestMinEvaluableRepetitionsRefusesOne is stated separately because it is
// the same hazard as an EvalSet declaring repetitionCount: 1 under a policy:
// spread computed from a single sample is not a measurement, and reporting
// spread=0 from it would be perfect stability, measured never.
func TestMinEvaluableRepetitionsRefusesOne(t *testing.T) {
	policy := validVariancePolicy()
	policy.MinEvaluableRepetitions = 1
	if err := policy.Validate(); !errors.Is(err, errInvalidDocument) {
		t.Fatalf("Validate() = %v, want one evaluable repetition to be refused", err)
	}
}

func TestDecodeVariancePolicyRejectsUnknownFields(t *testing.T) {
	raw := `{"formatVersion":1,"schema":"och.eval.variance-policy","id":"p","version":"v1",
"calibration":"uncalibrated","maxNumericSpread":0.2,"minVerdictStability":0.8,
"minEvaluableRepetitions":3,"apiKey":"secret"}`
	if _, err := DecodeVariancePolicy([]byte(raw)); err == nil {
		t.Fatal("an unknown field was accepted; a secret could ride along inside an unmodeled key")
	}
}

func TestDecodeVariancePolicyRejectsTheWrongSchemaOrFormat(t *testing.T) {
	wrongSchema := validVariancePolicy()
	wrongSchema.Schema = "och.eval.judge-config"
	if _, err := DecodeVariancePolicy(marshalPolicy(t, wrongSchema)); err == nil {
		t.Fatal("a document with another schema was accepted")
	}

	wrongFormat := validVariancePolicy()
	wrongFormat.FormatVersion = FormatVersion + 1
	if _, err := DecodeVariancePolicy(marshalPolicy(t, wrongFormat)); err == nil {
		t.Fatal("a document with an unsupported format version was accepted")
	}
}

func TestVariancePolicyRequiresIdentity(t *testing.T) {
	for name, mutate := range map[string]func(*VariancePolicy){
		"missing id":      func(p *VariancePolicy) { p.ID = "" },
		"blank id":        func(p *VariancePolicy) { p.ID = "   " },
		"missing version": func(p *VariancePolicy) { p.Version = "" },
	} {
		t.Run(name, func(t *testing.T) {
			policy := validVariancePolicy()
			mutate(&policy)
			if err := policy.Validate(); !errors.Is(err, errInvalidDocument) {
				t.Fatalf("Validate() = %v, want a refusal", err)
			}
		})
	}
}

// TestVariancePolicyDigestChangesWithEveryMeaningfulField keeps a Score's
// stated policy identity from matching a document that differs in a way that
// matters.
func TestVariancePolicyDigestChangesWithEveryMeaningfulField(t *testing.T) {
	base, err := VariancePolicyDigest(validVariancePolicy())
	if err != nil {
		t.Fatalf("VariancePolicyDigest: %v", err)
	}
	for name, mutate := range map[string]func(*VariancePolicy){
		"spread":      func(p *VariancePolicy) { p.MaxNumericSpread = 0.25 },
		"stability":   func(p *VariancePolicy) { p.MinVerdictStability = 0.9 },
		"repetitions": func(p *VariancePolicy) { p.MinEvaluableRepetitions = 5 },
		"version":     func(p *VariancePolicy) { p.Version = "v2" },
		"calibration": func(p *VariancePolicy) {
			p.Calibration = CalibrationCalibrated
			p.CalibratedFrom = "eval/artifacts/run"
		},
	} {
		t.Run(name, func(t *testing.T) {
			policy := validVariancePolicy()
			mutate(&policy)
			got, err := VariancePolicyDigest(policy)
			if err != nil {
				t.Fatalf("VariancePolicyDigest: %v", err)
			}
			if got == base {
				t.Fatalf("changing %s did not change the digest", name)
			}
		})
	}
}

// TestVariancePolicyCarriesNoCredentialField is a shape guard: this document
// is bound into evidence, so a credential-looking field must never be added
// to it without a deliberate decision.
func TestVariancePolicyCarriesNoCredentialField(t *testing.T) {
	data := marshalPolicy(t, validVariancePolicy())
	for _, forbidden := range []string{"apiKey", "api_key", "token", "secret", "password", "credential"} {
		if strings.Contains(strings.ToLower(string(data)), strings.ToLower(forbidden)) {
			t.Fatalf("the serialized policy contains %q", forbidden)
		}
	}
}
