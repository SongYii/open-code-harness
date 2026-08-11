package domain

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
)

const maxJSONLRecordSize = 1 << 20

func DecodeJSONL(reader io.Reader) ([]RecordedEvent, error) {
	trackedReader := &jsonlTrackingReader{reader: reader, line: 1, errorLine: 1}
	scanner := bufio.NewScanner(trackedReader)
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
		return nil, jsonlLineError(trackedReader.errorLine, invalidEventError("invalid JSONL stream"), scanner)
	}
	if len(records) == 0 {
		return nil, invalidEventError("JSONL stream is empty")
	}
	return records, nil
}

type jsonlTrackingReader struct {
	reader    io.Reader
	line      int
	errorLine int
}

func (reader *jsonlTrackingReader) Read(buffer []byte) (int, error) {
	read, err := reader.reader.Read(buffer)
	newlines := bytes.Count(buffer[:read], []byte{'\n'})
	if err != nil {
		reader.errorLine = reader.line + newlines
		if read > 0 && buffer[read-1] == '\n' {
			reader.errorLine--
		}
		if reader.errorLine < reader.line {
			reader.errorLine = reader.line
		}
	}
	reader.line += newlines
	return read, err
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
