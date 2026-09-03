package eval

import "fmt"

// LiveConfirmValue is the exact literal an operator's own trusted
// environment variable (this repository's own convention:
// OCH_EVAL_LIVE_CONFIRM, checked by the caller before ever calling
// RequireLiveConsent) must carry to complete design §24's live
// dual-consent gate. Requiring an exact, non-obvious literal rather than
// any truthy value is deliberate: a live run must never start by
// accident from an unrelated "true"/"1" left set in a shell.
const LiveConfirmValue = "I_UNDERSTAND"

// RequireLiveConsent enforces design §24's own live dual-consent gate:
// an EvalSet's own declared lane and a caller's own explicit liveFlag
// must agree exactly, and a live lane additionally requires
// confirmEnvValue (the exact string the caller itself read from its own
// trusted environment variable — this function never reads an
// environment variable itself) to equal LiveConfirmValue. Every check
// here must run, and every one must pass, before a caller resolves or
// reads any credential (design: "Either condition missing rejects
// startup before reading a credential") — RequireLiveConsent itself
// never touches a credential, so calling it first and refusing to
// proceed past a non-nil error is what makes that guarantee real.
func RequireLiveConsent(lane EvalLane, liveFlag bool, confirmEnvValue string) error {
	switch lane {
	case LaneFixture:
		if liveFlag {
			return fmt.Errorf("eval: live consent: -live was passed but this EvalSet's own lane is %q", lane)
		}
		return nil
	case LaneLive:
		if !liveFlag {
			return fmt.Errorf("eval: live consent: this EvalSet's own lane is %q: explicit live confirmation is required", lane)
		}
		if confirmEnvValue != LiveConfirmValue {
			return fmt.Errorf("eval: live consent: live lane requires an explicit environment confirmation before any credential is read")
		}
		return nil
	default:
		return fmt.Errorf("eval: live consent: unknown lane %q", lane)
	}
}
