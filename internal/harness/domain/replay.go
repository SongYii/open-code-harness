package domain

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

const maxJSONLRecordSize = 1 << 20

func DecodeJSONL(reader io.Reader) ([]RecordedEvent, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), maxJSONLRecordSize)

	var records []RecordedEvent
	line := 0
	for scanner.Scan() {
		line++
		if strings.TrimSpace(scanner.Text()) == "" {
			return nil, fmt.Errorf("JSONL line %d: %w", line, invalidEventError("blank JSONL line"))
		}
		record, err := UnmarshalRecordedEvent(scanner.Bytes())
		if err != nil {
			return nil, fmt.Errorf("JSONL line %d: %w", line, err)
		}
		records = append(records, record)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("JSONL line %d: %w", line+1, invalidEventError("invalid JSONL stream"))
	}
	if len(records) == 0 {
		return nil, invalidEventError("JSONL stream is empty")
	}
	return records, nil
}

func Replay(records []RecordedEvent) (Session, error) {
	var state Session
	for _, record := range records {
		next, err := Apply(state, record)
		if err != nil {
			return Session{}, fmt.Errorf("replay sequence %d: %w", record.Sequence, err)
		}
		state = next
	}
	return state, nil
}
