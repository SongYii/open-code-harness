package domain

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"
)

const maxJSONLRecordSize = 1 << 20

func DecodeJSONL(reader io.Reader) ([]RecordedEvent, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), maxJSONLRecordSize+2)

	var records []RecordedEvent
	line := 0
	for scanner.Scan() {
		line++
		if len(scanner.Bytes()) > maxJSONLRecordSize {
			return nil, jsonlLineError(line, invalidEventError("JSONL record exceeds 1 MiB"), scanner)
		}
		if strings.TrimSpace(scanner.Text()) == "" {
			return nil, jsonlLineError(line, invalidEventError("blank JSONL line"), scanner)
		}
		record, err := UnmarshalRecordedEvent(scanner.Bytes())
		if err != nil {
			return nil, jsonlLineError(line, err, scanner)
		}
		records = append(records, record)
	}
	if err := scanner.Err(); err != nil {
		return nil, jsonlLineError(line+1, invalidEventError("invalid JSONL stream"), scanner)
	}
	if len(records) == 0 {
		return nil, invalidEventError("JSONL stream is empty")
	}
	return records, nil
}

func jsonlLineError(line int, err error, scanner *bufio.Scanner) error {
	if scannerErr := scanner.Err(); scannerErr != nil {
		err = errors.Join(err, scannerErr)
	}
	return fmt.Errorf("JSONL line %d: %w", line, err)
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
