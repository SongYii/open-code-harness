package eval

import "testing"

func TestRequireLiveConsentAcceptsFixtureLaneWithoutLiveFlag(t *testing.T) {
	if err := RequireLiveConsent(LaneFixture, false, ""); err != nil {
		t.Fatalf("RequireLiveConsent() error = %v, want nil", err)
	}
}

func TestRequireLiveConsentRejectsLiveFlagOnFixtureLane(t *testing.T) {
	if err := RequireLiveConsent(LaneFixture, true, LiveConfirmValue); err == nil {
		t.Fatal("RequireLiveConsent() error = nil, want a refusal: -live must not be accepted for a fixture-lane EvalSet")
	}
}

func TestRequireLiveConsentRejectsLiveLaneWithoutLiveFlag(t *testing.T) {
	if err := RequireLiveConsent(LaneLive, false, LiveConfirmValue); err == nil {
		t.Fatal("RequireLiveConsent() error = nil, want a refusal: a live lane requires the explicit liveFlag half of consent")
	}
}

func TestRequireLiveConsentRejectsLiveLaneWithoutEnvironmentConfirmation(t *testing.T) {
	if err := RequireLiveConsent(LaneLive, true, ""); err == nil {
		t.Fatal("RequireLiveConsent() error = nil, want a refusal: a live lane requires the explicit environment-confirmation half of consent")
	}
	if err := RequireLiveConsent(LaneLive, true, "true"); err == nil {
		t.Fatal("RequireLiveConsent() error = nil, want a refusal: only the exact LiveConfirmValue literal satisfies consent, not any truthy value")
	}
}

func TestRequireLiveConsentAcceptsLiveLaneWithBothConsentHalves(t *testing.T) {
	if err := RequireLiveConsent(LaneLive, true, LiveConfirmValue); err != nil {
		t.Fatalf("RequireLiveConsent() error = %v, want nil", err)
	}
}

func TestRequireLiveConsentRejectsUnknownLane(t *testing.T) {
	if err := RequireLiveConsent(EvalLane("bogus"), false, ""); err == nil {
		t.Fatal("RequireLiveConsent() error = nil, want a refusal for an unknown lane")
	}
}
