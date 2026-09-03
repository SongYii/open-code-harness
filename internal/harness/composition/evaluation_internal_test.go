package composition

import "testing"

// TestVerifyAuditIncludesSessionHead is the implementation plan Task 4
// mutation check, made directly testable rather than requiring a manual
// remove-and-restore: it proves the inclusion-proof comparison itself
// rejects a deliberately short regenerated audit head (one that falls short
// of the append that introduced the pinned Session head) and accepts one
// that reaches or exceeds it. If this comparison were ever deleted from
// ExportEvaluationEvidence, this test would keep passing on its own (it
// calls the function directly) -- TestExportEvaluationEvidenceHappyPath in
// evaluation_test.go is what would catch that deletion, since it exercises
// the real call site end to end.
func TestVerifyAuditIncludesSessionHead(t *testing.T) {
	tests := []struct {
		name                            string
		auditHeadCommitPosition         uint64
		sessionHeadAppendCommitPosition uint64
		wantErr                         bool
	}{
		{"audit reaches exactly the session head append", 5, 5, false},
		{"audit reaches past the session head append", 8, 5, false},
		{"audit falls short of the session head append", 3, 5, true},
		{"audit is empty but a session head append is expected", 0, 1, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := verifyAuditIncludesSessionHead(test.auditHeadCommitPosition, test.sessionHeadAppendCommitPosition)
			if (err != nil) != test.wantErr {
				t.Fatalf("verifyAuditIncludesSessionHead(%d, %d) error = %v, wantErr %t",
					test.auditHeadCommitPosition, test.sessionHeadAppendCommitPosition, err, test.wantErr)
			}
		})
	}
}
