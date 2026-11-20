package event

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	_ "modernc.org/sqlite"
)

// Store is an append-only event log in SQLite. It stands in for the
// partitioned bus + event store of ADR-0002: one total order (seq), durable,
// replayable, with per-consumer offsets.
type Store struct {
	db      *sql.DB
	catalog *Catalog
}

const schema = `
CREATE TABLE IF NOT EXISTS events (
  seq            INTEGER PRIMARY KEY AUTOINCREMENT,
  id             TEXT NOT NULL UNIQUE,
  type           TEXT NOT NULL,
  subject        TEXT NOT NULL,
  correlationid  TEXT NOT NULL,
  causationid    TEXT NOT NULL,
  time           TEXT NOT NULL,
  envelope       TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS events_corr ON events(correlationid, seq);
CREATE INDEX IF NOT EXISTS events_type ON events(type, seq);
CREATE TABLE IF NOT EXISTS offsets (
  consumer TEXT PRIMARY KEY,
  seq      INTEGER NOT NULL
);`

// Open opens (or creates) the log at path. The catalog gate rejects any
// publish whose type is not catalogued (docs/03 §9, H-PLT-01).
func Open(path string, catalog *Catalog) (*Store, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // SQLite: one writer; keeps ordering simple.
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}
	return &Store{db: db, catalog: catalog}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// ErrUncatalogued is returned when a type is not in the catalog.
var ErrUncatalogued = errors.New("event type not in catalog")

// Publish appends events atomically. Events whose ID already exists are
// skipped (idempotent re-publish); it reports how many were new.
func (s *Store) Publish(ctx context.Context, evs ...*Event) (int, error) {
	for _, e := range evs {
		if s.catalog != nil && !s.catalog.Has(e.Type) {
			return 0, fmt.Errorf("%w: %s", ErrUncatalogued, e.Type)
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	added := 0
	for _, e := range evs {
		env, err := json.Marshal(e)
		if err != nil {
			return 0, err
		}
		res, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO events(id,type,subject,correlationid,causationid,time,envelope) VALUES(?,?,?,?,?,?,?)`,
			e.ID, e.Type, e.Subject, e.CorrelationID, e.CausationID, e.Time.Format("2006-01-02T15:04:05.000Z07:00"), string(env))
		if err != nil {
			return 0, fmt.Errorf("insert %s: %w", e.Type, err)
		}
		if n, _ := res.RowsAffected(); n == 1 {
			added++
		}
	}
	return added, tx.Commit()
}

func scan(rows *sql.Rows) ([]*Event, error) {
	defer rows.Close()
	var out []*Event
	for rows.Next() {
		var seq int64
		var env string
		if err := rows.Scan(&seq, &env); err != nil {
			return nil, err
		}
		e := &Event{}
		if err := json.Unmarshal([]byte(env), e); err != nil {
			return nil, err
		}
		e.Seq = seq
		out = append(out, e)
	}
	return out, rows.Err()
}

// After returns up to limit events with seq > after, optionally filtered to types.
func (s *Store) After(ctx context.Context, after int64, types []string, limit int) ([]*Event, error) {
	q := `SELECT seq, envelope FROM events WHERE seq > ?`
	args := []any{after}
	if len(types) > 0 {
		q += ` AND type IN (?` + strings.Repeat(",?", len(types)-1) + `)`
		for _, t := range types {
			args = append(args, t)
		}
	}
	q += ` ORDER BY seq LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	return scan(rows)
}

// Correlation returns every event of one correlation in log order.
func (s *Store) Correlation(ctx context.Context, corr string) ([]*Event, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT seq, envelope FROM events WHERE correlationid = ? ORDER BY seq`, corr)
	if err != nil {
		return nil, err
	}
	return scan(rows)
}

// Offset returns a consumer's committed position.
func (s *Store) Offset(ctx context.Context, consumer string) (int64, error) {
	var seq int64
	err := s.db.QueryRowContext(ctx, `SELECT seq FROM offsets WHERE consumer = ?`, consumer).Scan(&seq)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return seq, err
}

// Commit records that consumer has processed everything up to seq.
func (s *Store) Commit(ctx context.Context, consumer string, seq int64) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO offsets(consumer, seq) VALUES(?, ?) ON CONFLICT(consumer) DO UPDATE SET seq = excluded.seq`, consumer, seq)
	return err
}
