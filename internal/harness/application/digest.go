package application

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

const (
	digestFormatVersion = uint64(1)
	maxEventPayloadSize = 8 * 1024 * 1024
	maxAppendDigestSize = 16 * 1024 * 1024
	maxAppendEvents     = 64
)

// DigestAppendRequest returns the version-1 canonical digest for an immutable
// append request. Receipt and authority facts are validated but not encoded.
func DigestAppendRequest(request AppendRequestV2) (Digest, error) {
	events, err := validateAppendRequestForDigest(request)
	if err != nil {
		return Digest{}, err
	}

	encoder := framedDigestEncoder{limit: maxAppendDigestSize}
	encoder.writeUint64(digestFormatVersion)
	encoder.writeString(string(request.SessionID))
	encoder.writeUint64(request.ExpectedVersion)
	encoder.writeString(string(request.CommandID))
	if request.Admission == nil {
		encoder.writeByte(0)
	} else {
		encoder.writeByte(1)
		encoder.writeString(string(request.Admission.RunTurnRequestID))
		encoder.writeBytes(request.Admission.RequestDigest[:])
		encoder.writeString(string(request.Admission.TurnID))
		encoder.writeString(string(request.Admission.ItemID))
	}
	encoder.writeUint64(uint64(len(events)))
	for _, event := range events {
		encoder.writeString(string(event.ID))
		encoder.writeString(event.eventType)
		encoder.writeUint64(uint64(event.SchemaVersion))
		encoder.writeString(event.OccurredAt.Format(time.RFC3339Nano))
		encoder.writeBytes(event.payload)
	}
	if err := encoder.err; err != nil {
		return Digest{}, err
	}
	return sha256.Sum256(encoder.bytes()), nil
}

// DigestRunTurnRequestV1 returns the version-1 digest for the Session and the
// exact UTF-8 input of a RunTurn request.
func DigestRunTurnRequestV1(sessionID domain.SessionID, input string) (Digest, error) {
	if _, err := domain.ParseSessionID(string(sessionID)); err != nil {
		return Digest{}, fmt.Errorf("invalid session ID: %w", err)
	}
	if !utf8.ValidString(input) {
		return Digest{}, fmt.Errorf("run turn input must be valid UTF-8")
	}
	encoder := framedDigestEncoder{}
	encoder.writeUint64(digestFormatVersion)
	encoder.writeString(string(sessionID))
	encoder.writeString(input)
	if err := encoder.err; err != nil {
		return Digest{}, err
	}
	return sha256.Sum256(encoder.bytes()), nil
}

// ParseDigest accepts only the canonical lower-case hexadecimal Digest form.
func ParseDigest(text string) (Digest, error) {
	var digest Digest
	if len(text) != hex.EncodedLen(len(digest)) {
		return Digest{}, fmt.Errorf("digest must be %d lowercase hexadecimal characters", hex.EncodedLen(len(digest)))
	}
	for _, value := range text {
		if (value < '0' || value > '9') && (value < 'a' || value > 'f') {
			return Digest{}, fmt.Errorf("digest must be lowercase hexadecimal")
		}
	}
	if _, err := hex.Decode(digest[:], []byte(text)); err != nil {
		return Digest{}, fmt.Errorf("invalid digest: %w", err)
	}
	return digest, nil
}

type canonicalProposedEvent struct {
	ID            domain.EventID
	SchemaVersion uint32
	OccurredAt    time.Time
	eventType     string
	payload       []byte
}

func validateAppendRequestForDigest(request AppendRequestV2) ([]canonicalProposedEvent, error) {
	if _, err := domain.ParseAppendID(string(request.AppendID)); err != nil {
		return nil, fmt.Errorf("invalid append ID: %w", err)
	}
	if _, err := domain.ParseSessionID(string(request.SessionID)); err != nil {
		return nil, fmt.Errorf("invalid session ID: %w", err)
	}
	if _, err := domain.ParseCommandID(string(request.CommandID)); err != nil {
		return nil, fmt.Errorf("invalid command ID: %w", err)
	}
	if err := request.Authority.Validate(); err != nil {
		return nil, fmt.Errorf("invalid writer authority: %w", err)
	}
	if request.Admission != nil {
		if _, err := domain.ParseRunTurnRequestID(string(request.Admission.RunTurnRequestID)); err != nil {
			return nil, fmt.Errorf("invalid admission request ID: %w", err)
		}
		if _, err := domain.ParseTurnID(string(request.Admission.TurnID)); err != nil {
			return nil, fmt.Errorf("invalid admission turn ID: %w", err)
		}
		if _, err := domain.ParseItemID(string(request.Admission.ItemID)); err != nil {
			return nil, fmt.Errorf("invalid admission item ID: %w", err)
		}
	}
	if len(request.Events) == 0 || len(request.Events) > maxAppendEvents {
		return nil, fmt.Errorf("append must contain between 1 and %d events", maxAppendEvents)
	}

	events := make([]canonicalProposedEvent, len(request.Events))
	for index, event := range request.Events {
		if _, err := domain.ParseEventID(string(event.ID)); err != nil {
			return nil, fmt.Errorf("invalid event %d ID: %w", index, err)
		}
		if event.SchemaVersion != 1 {
			return nil, fmt.Errorf("event %d has unsupported schema version", index)
		}
		if event.OccurredAt.IsZero() || event.OccurredAt.Location() != time.UTC {
			return nil, fmt.Errorf("event %d timestamp must be non-zero UTC", index)
		}
		timestamp := event.OccurredAt.Format(time.RFC3339Nano)
		if _, err := time.Parse(time.RFC3339Nano, timestamp); err != nil {
			return nil, fmt.Errorf("event %d timestamp is outside RFC3339Nano range", index)
		}
		eventType, payload, err := domain.MarshalEventPayload(event.Event)
		if err != nil {
			return nil, fmt.Errorf("invalid event %d payload: %w", index, err)
		}
		if len(payload) > maxEventPayloadSize {
			return nil, fmt.Errorf("event %d payload exceeds %d bytes", index, maxEventPayloadSize)
		}
		events[index] = canonicalProposedEvent{
			ID:            event.ID,
			SchemaVersion: event.SchemaVersion,
			OccurredAt:    event.OccurredAt,
			eventType:     eventType,
			payload:       payload,
		}
	}
	return events, nil
}

type framedDigestEncoder struct {
	data  []byte
	limit int
	err   error
}

func (encoder *framedDigestEncoder) writeByte(value byte) {
	encoder.writeRaw([]byte{value})
}

func (encoder *framedDigestEncoder) writeUint64(value uint64) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], value)
	encoder.writeRaw(encoded[:])
}

func (encoder *framedDigestEncoder) writeString(value string) {
	encoder.writeBytes([]byte(value))
}

func (encoder *framedDigestEncoder) writeBytes(value []byte) {
	if len(value) > int(^uint32(0)) {
		encoder.setError(fmt.Errorf("framed value exceeds uint32 length"))
		return
	}
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(value)))
	encoder.writeRaw(length[:])
	encoder.writeRaw(value)
}

func (encoder *framedDigestEncoder) writeRaw(value []byte) {
	if encoder.err != nil {
		return
	}
	if encoder.limit > 0 && len(value) > encoder.limit-len(encoder.data) {
		encoder.setError(fmt.Errorf("canonical append request exceeds %d bytes", encoder.limit))
		return
	}
	encoder.data = append(encoder.data, value...)
}

func (encoder *framedDigestEncoder) setError(err error) {
	if encoder.err == nil {
		encoder.err = err
	}
}

func (encoder *framedDigestEncoder) bytes() []byte { return encoder.data }
