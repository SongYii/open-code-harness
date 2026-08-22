package system

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

// Clock reads the host wall clock. Times are returned in UTC so that a
// recorded event never carries a local offset.
type Clock struct{}

var _ application.Clock = Clock{}

func (Clock) Now() time.Time { return time.Now().UTC() }

// idBytes is the random suffix width. 128 bits makes collision across a
// deployment's lifetime not worth reasoning about, and keeps an identifier
// short enough to read in a log line.
const idBytes = 16

// IDs generates opaque identifiers from the system random source.
//
// Identifiers are prefixed by kind so that a value appearing in a log or a
// database row says what it is. The prefix is presentation only: nothing in
// Domain or Application parses it, and no code may start doing so.
type IDs struct{}

var _ application.IDGenerator = IDs{}

// newID returns "<kind>-<hex>" or an error. A failure to read the system
// random source is returned rather than papered over with a weaker source:
// identifiers carry admission and append identity, so a predictable one is
// worse than a failed command.
func newID(kind string) (string, error) {
	raw := make([]byte, idBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("system: generate %s id: %w", kind, err)
	}
	return kind + "-" + hex.EncodeToString(raw), nil
}

func (IDs) NewSessionID() (domain.SessionID, error) {
	value, err := newID("session")
	if err != nil {
		return "", err
	}
	return domain.ParseSessionID(value)
}

func (IDs) NewTurnID() (domain.TurnID, error) {
	value, err := newID("turn")
	if err != nil {
		return "", err
	}
	return domain.ParseTurnID(value)
}

func (IDs) NewItemID() (domain.ItemID, error) {
	value, err := newID("item")
	if err != nil {
		return "", err
	}
	return domain.ParseItemID(value)
}

func (IDs) NewCommandID() (domain.CommandID, error) {
	value, err := newID("command")
	if err != nil {
		return "", err
	}
	return domain.ParseCommandID(value)
}

func (IDs) NewAppendID() (domain.AppendID, error) {
	value, err := newID("append")
	if err != nil {
		return "", err
	}
	return domain.ParseAppendID(value)
}

func (IDs) NewEventID() (domain.EventID, error) {
	value, err := newID("event")
	if err != nil {
		return "", err
	}
	return domain.ParseEventID(value)
}

func (IDs) NewApprovalID() (domain.ApprovalID, error) {
	value, err := newID("approval")
	if err != nil {
		return "", err
	}
	return domain.ParseApprovalID(value)
}
