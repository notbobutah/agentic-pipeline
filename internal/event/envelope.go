// Package event implements the CloudEvents envelope (docs/03 §4), an
// append-only SQLite event log, a catalog gate and an in-process dispatcher.
package event

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Actor identifies on whose behalf an event happened.
type Actor struct {
	Kind  string `json:"kind"` // human | bot | agent | system
	ID    string `json:"id"`
	Login string `json:"login,omitempty"`
}

// Event is the canonical envelope. Field names follow CloudEvents 1.0 plus the
// platform extensions in docs/schemas/envelope.schema.json.
type Event struct {
	SpecVersion     string          `json:"specversion"`
	ID              string          `json:"id"`
	Source          string          `json:"source"`
	Type            string          `json:"type"`
	Subject         string          `json:"subject"`
	Time            time.Time       `json:"time"`
	DataContentType string          `json:"datacontenttype"`
	DataSchema      string          `json:"dataschema"`
	Tenant          string          `json:"tenant"`
	Team            string          `json:"team"`
	Repo            string          `json:"repo"`
	PartitionKey    string          `json:"partitionkey"`
	CorrelationID   string          `json:"correlationid"`
	CausationID     string          `json:"causationid"`
	TraceParent     string          `json:"traceparent"`
	Origin          string          `json:"origin,omitempty"`
	Actor           *Actor          `json:"actor,omitempty"`
	Data            json.RawMessage `json:"data"`

	// Seq is the log position, assigned by the store. Not part of the envelope.
	Seq int64 `json:"-"`
}

// Defaults applied to every event built in this process.
var (
	Tenant = "demo"
	Team   = "demo"
)

// namespace for deterministic (UUIDv5) event IDs.
var idNamespace = uuid.MustParse("6f1c2d8e-4b7a-4e0f-9c3d-2a5b8e7f1a60")

// Spec describes a new event. Exactly one of Root or Cause should be set:
// a root event starts a new correlation; a caused event inherits it.
type Spec struct {
	Type    string
	Subject string
	Source  string
	Repo    string
	Origin  string
	Actor   *Actor
	Data    any

	// Root events only.
	CorrelationID string

	// Caused events only.
	Cause *Event

	// Key makes the event ID deterministic: the same (cause, type, key) always
	// yields the same ID, so a handler that is re-run after a crash publishes
	// duplicates that the store ignores. Root events may set it too (e.g. the
	// provider delivery ID) to dedupe ingestion.
	Key string
}

// New builds an envelope from a spec.
func New(s Spec) (*Event, error) {
	data, err := json.Marshal(s.Data)
	if err != nil {
		return nil, fmt.Errorf("marshal %s data: %w", s.Type, err)
	}
	if s.Data == nil {
		data = []byte("{}")
	}
	e := &Event{
		SpecVersion:     "1.0",
		Source:          s.Source,
		Type:            s.Type,
		Subject:         s.Subject,
		Time:            time.Now().UTC().Truncate(time.Millisecond),
		DataContentType: "application/json",
		DataSchema:      "ap://schemas/" + trimVersion(s.Type) + "/1.0.0",
		Tenant:          Tenant,
		Team:            Team,
		Repo:            s.Repo,
		Origin:          s.Origin,
		Actor:           s.Actor,
		Data:            data,
	}
	if e.Repo == "" {
		e.Repo = "_platform"
	}
	if e.Source == "" {
		e.Source = "//ap/system"
	}
	if s.Cause != nil {
		e.CorrelationID = s.Cause.CorrelationID
		e.CausationID = s.Cause.ID
		e.TraceParent = childTrace(s.Cause.TraceParent)
	} else {
		if s.CorrelationID == "" {
			return nil, fmt.Errorf("root event %s needs a correlation id", s.Type)
		}
		e.CorrelationID = s.CorrelationID
		e.TraceParent = newTrace()
	}
	// Work-plane events are ordered per work item; repo events per repo.
	e.PartitionKey = e.Repo
	if e.Repo == "_platform" {
		e.PartitionKey = e.CorrelationID
	}
	if s.Key != "" {
		e.ID = uuid.NewSHA1(idNamespace, []byte(e.CausationID+"|"+e.Type+"|"+s.Key)).String()
	} else {
		e.ID = uuid.Must(uuid.NewV7()).String()
	}
	return e, nil
}

// Decode unmarshals the event's data into v.
func (e *Event) Decode(v any) error {
	if err := json.Unmarshal(e.Data, v); err != nil {
		return fmt.Errorf("decode %s %s: %w", e.Type, e.ID, err)
	}
	return nil
}

// trimVersion turns "ap.work.plan.approved.v1" into "ap.work.plan.approved".
func trimVersion(t string) string {
	for i := len(t) - 1; i >= 0; i-- {
		if t[i] == '.' {
			if len(t)-i > 2 && t[i+1] == 'v' {
				return t[:i]
			}
			break
		}
	}
	return t
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func newTrace() string { return "00-" + randHex(16) + "-" + randHex(8) + "-01" }

// childTrace keeps the trace id and mints a new span id.
func childTrace(parent string) string {
	if len(parent) != 55 {
		return newTrace()
	}
	return parent[:36] + randHex(8) + "-01"
}
