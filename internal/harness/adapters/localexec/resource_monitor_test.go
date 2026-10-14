package localexec

import (
	"errors"
	"math"
	"testing"
)

func TestUsageLedgerRetainsDepartedCPUAndSeparatesPIDReuse(t *testing.T) {
	ledger := make(usageLedger)
	first := processIdentity{pid: 42, start: 1}
	second := processIdentity{pid: 42, start: 2}
	if cpu, rss, err := ledger.sample([]processUsage{{identity: first, cpu: 60, resident: 40}}); err != nil || cpu != 60 || rss != 40 {
		t.Fatalf("first sample = %d/%d, %v", cpu, rss, err)
	}
	// Replacing a worker cannot reset its spent CPU time; departed RSS
	// must nevertheless be released, not charged forever like CPU.
	if cpu, rss, err := ledger.sample([]processUsage{{identity: second, cpu: 50, resident: 30}}); err != nil || cpu != 110 || rss != 30 {
		t.Fatalf("reused PID sample = %d/%d, %v", cpu, rss, err)
	}
	if cpu, rss, err := ledger.sample(nil); err != nil || cpu != 110 || rss != 0 {
		t.Fatalf("departed workers sample = %d/%d, %v", cpu, rss, err)
	}
}

func TestUsageLedgerOverflowCannotDisableAQuota(t *testing.T) {
	ledger := make(usageLedger)
	usages := []processUsage{
		{identity: processIdentity{pid: 1}, cpu: math.MaxUint64, resident: math.MaxUint64},
		{identity: processIdentity{pid: 2}, cpu: 10, resident: 10},
	}
	cpu, rss, err := ledger.sample(usages)
	if err != nil || cpu != math.MaxUint64 || rss != math.MaxUint64 {
		t.Fatalf("overflow disabled accounting: %d/%d %v", cpu, rss, err)
	}
}

func TestRequireEnforcementNeverSubstitutesMonitoringForFull(t *testing.T) {
	actual := Enforcement{Memory: EnforcementPartial, CPU: EnforcementPartial}
	for _, required := range []Enforcement{{Memory: EnforcementFull}, {CPU: EnforcementFull}} {
		if err := requireEnforcement(actual, required); !errors.Is(err, ErrEnforcementUnavailable) {
			t.Fatalf("partial accepted strict requirement %+v: %v", required, err)
		}
	}
	if err := requireEnforcement(actual, Enforcement{CPU: EnforcementPartial, Memory: EnforcementPartial}); err != nil {
		t.Fatal(err)
	}
	if err := requireEnforcement(actual, Enforcement{CPU: "unknown"}); err == nil {
		t.Fatal("unknown contract accepted")
	}
}

func TestGroupIdentityRejectsReusedOrUnprovenMembers(t *testing.T) {
	root := processIdentity{pid: 42, start: 1}
	worker := processIdentity{pid: 43, start: 2}
	known := map[processIdentity]bool{root: true, worker: true}
	if !groupIdentityMatches(42, 1, known, []processUsage{{identity: root}}) {
		t.Fatal("original leader refused")
	}
	if !groupIdentityMatches(42, 1, known, []processUsage{{identity: worker}}) {
		t.Fatal("known descendant refused after leader exit")
	}
	for _, identity := range []processIdentity{{pid: 42, start: 3}, {pid: 44, start: 4}} {
		if groupIdentityMatches(42, 1, known, []processUsage{{identity: identity}}) {
			t.Fatalf("unrelated generation accepted: %+v", identity)
		}
	}
}
