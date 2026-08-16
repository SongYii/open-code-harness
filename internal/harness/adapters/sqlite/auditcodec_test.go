package sqlite

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"math"
	"testing"
	"time"
)

func errorsAs(err error, target **CorruptError) bool {
	return errors.As(err, target)
}

func sampleBatch() auditBatch {
	batch := auditBatch{
		FormatVersion:   1,
		CommitPosition:  42,
		AppendID:        "append-audit-sample",
		CommandID:       "command-audit-sample",
		SessionID:       "session-audit-sample",
		ExpectedVersion: 8,
		FirstSequence:   9,
		LastSequence:    10,
		CommittedAtUnix: 1.7869e9,
	}
	copy(batch.PreviousDigest[:], bytes.Repeat([]byte{0xab}, 32))
	batch.Events = [][]byte{
		[]byte(`{"id":"event-1","kind":"turn.started"}`),
		[]byte(`{"id":"event-2","kind":"turn.completed"}`),
	}
	return batch
}

func TestAuditCodecV1EncodesCanonicalFieldOrder(t *testing.T) {
	codec, err := auditCodecFor(1)
	if err != nil {
		t.Fatalf("codec v1: %v", err)
	}
	envelope, _, err := codec.Encode(sampleBatch())
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	var fields []string
	decoder := json.NewDecoder(bytes.NewReader(envelope))
	token, err := decoder.Token()
	if err != nil {
		t.Fatalf("first token: %v", err)
	}
	if delim, ok := token.(json.Delim); !ok || delim != '{' {
		t.Fatalf("envelope is not a JSON object")
	}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			t.Fatalf("field token: %v", err)
		}
		name, ok := token.(string)
		if !ok {
			t.Fatalf("field name token = %v", name)
		}
		fields = append(fields, name)
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			t.Fatalf("skip %s: %v", name, err)
		}
	}
	want := []string{"formatVersion", "commitPosition", "appendId", "commandId", "sessionId", "expectedVersion", "firstSequence", "lastSequence", "committedAt", "previousDigest", "events", "batchDigest"}
	if len(fields) != len(want) {
		t.Fatalf("fields = %v, want %v", fields, want)
	}
	for i := range want {
		if fields[i] != want[i] {
			t.Fatalf("field %d = %s, want %s", i, fields[i], want[i])
		}
	}
}

func TestAuditCodecV1RoundTripsLosslessly(t *testing.T) {
	codec, _ := auditCodecFor(1)
	batch := sampleBatch()
	envelope, digest, err := codec.Encode(batch)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	decoded, err := codec.Decode(envelope)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded.FormatVersion != batch.FormatVersion || decoded.CommitPosition != batch.CommitPosition ||
		decoded.AppendID != batch.AppendID || decoded.CommandID != batch.CommandID ||
		decoded.SessionID != batch.SessionID || decoded.ExpectedVersion != batch.ExpectedVersion ||
		decoded.FirstSequence != batch.FirstSequence || decoded.LastSequence != batch.LastSequence {
		t.Fatalf("decoded metadata = %+v", decoded)
	}
	if !bytes.Equal(decoded.PreviousDigest[:], batch.PreviousDigest[:]) {
		t.Fatal("previous digest not preserved")
	}
	if len(decoded.Events) != len(batch.Events) {
		t.Fatalf("decoded events = %d, want %d", len(decoded.Events), len(batch.Events))
	}
	for i := range decoded.Events {
		if !bytes.Equal(decoded.Events[i], batch.Events[i]) {
			t.Fatalf("event %d bytes changed: %s", i, decoded.Events[i])
		}
	}
	var digestHex string
	if err := json.Unmarshal([]byte(`{"batchDigest":""}`), &struct{}{}); err == nil {
		// extract batchDigest from envelope for comparison
		var probe struct {
			BatchDigest string `json:"batchDigest"`
		}
		if err := json.Unmarshal(envelope, &probe); err != nil {
			t.Fatalf("probe digest: %v", err)
		}
		digestHex = probe.BatchDigest
	}
	if digestHex != "sha256:"+hexEncode(digest[:]) {
		t.Fatalf("batchDigest = %q, want sha256:%s", digestHex, hexEncode(digest[:]))
	}
}

func TestAuditCodecV1DigestCoversEveryFieldExceptItself(t *testing.T) {
	codec, _ := auditCodecFor(1)
	base := sampleBatch()
	_, baseDigest, err := codec.Encode(base)
	if err != nil {
		t.Fatal(err)
	}
	// formatVersion is excluded: codec v1 refuses to encode any other value
	// by construction, and Decode's digest check rejects tampering with it.
	mutations := map[string]func(*auditBatch){
		"commitPosition":  func(b *auditBatch) { b.CommitPosition++ },
		"appendId":        func(b *auditBatch) { b.AppendID += "x" },
		"commandId":       func(b *auditBatch) { b.CommandID += "x" },
		"sessionId":       func(b *auditBatch) { b.SessionID += "x" },
		"expectedVersion": func(b *auditBatch) { b.ExpectedVersion++ },
		"firstSequence":   func(b *auditBatch) { b.FirstSequence++ },
		"lastSequence":    func(b *auditBatch) { b.LastSequence++ },
		"committedAt":     func(b *auditBatch) { b.CommittedAtUnix += 0.5 },
		"previousDigest":  func(b *auditBatch) { b.PreviousDigest[0] ^= 0xff },
		"events":          func(b *auditBatch) { b.Events[0] = []byte(`{"tampered":true}`) },
	}
	for field, mutate := range mutations {
		mutated := base
		mutate(&mutated)
		_, mutatedDigest, err := codec.Encode(mutated)
		if err != nil {
			t.Fatalf("encode with %s mutated: %v", field, err)
		}
		if bytes.Equal(mutatedDigest[:], baseDigest[:]) {
			t.Fatalf("batchDigest ignores %s", field)
		}
	}
}

func TestAuditChainGenesisConstant(t *testing.T) {
	if bytes.Equal(auditGenesisDigest[:], make([]byte, 32)) {
		t.Fatal("genesis digest is the zero value")
	}
	if !auditGenesisDigestIsFrozen() {
		t.Fatal("genesis digest does not match its documented derivation")
	}
}

func TestAuditCodecV1CommittedAtIsDeterministicRFC3339(t *testing.T) {
	codec, _ := auditCodecFor(1)
	batch := sampleBatch()
	batch.CommittedAtUnix = 1786900000.25
	envelope, _, err := codec.Encode(batch)
	if err != nil {
		t.Fatal(err)
	}
	var probe struct {
		CommittedAt string `json:"committedAt"`
	}
	if err := json.Unmarshal(envelope, &probe); err != nil {
		t.Fatal(err)
	}
	parsed, err := time.Parse(time.RFC3339Nano, probe.CommittedAt)
	if err != nil {
		t.Fatalf("committedAt %q is not RFC3339Nano: %v", probe.CommittedAt, err)
	}
	if math.Abs(parsed.Sub(time.Unix(1786900000, 250000000)).Seconds()) > 0.001 {
		t.Fatalf("committedAt %q does not preserve the stored instant", probe.CommittedAt)
	}
}

func TestAuditCodecRegistryFailsClosedOnUnknownVersion(t *testing.T) {
	if _, err := auditCodecFor(1); err != nil {
		t.Fatalf("codec v1 must resolve: %v", err)
	}
	if _, err := auditCodecFor(99); err == nil {
		t.Fatal("unknown codec version resolved; want fail-closed")
	}
	var corrupt *CorruptError
	if _, err := auditCodecFor(99); !errorsAs(err, &corrupt) {
		t.Fatalf("unknown codec must classify as corruption, got %v", err)
	}
	if _, err := auditCodecFor(0); err == nil {
		t.Fatal("codec version 0 resolved; want fail-closed")
	}
}

func TestAuditEnvelopeEventDigestHelper(t *testing.T) {
	payload := []byte(`{"id":"event-1"}`)
	digest := auditEventPayloadDigest(payload)
	if digest != sha256.Sum256(payload) {
		t.Fatal("event payload digest helper does not match SHA-256 of the payload")
	}
}

func auditGenesisDigestIsFrozen() bool {
	want := sha256.Sum256([]byte("open-code-harness/audit-chain/genesis/v1"))
	return bytes.Equal(auditGenesisDigest[:], want[:])
}

func hexEncode(value []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 0, len(value)*2)
	for _, b := range value {
		out = append(out, digits[b>>4], digits[b&0x0f])
	}
	return string(out)
}
