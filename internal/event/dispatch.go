package event

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// Handler is a named consumer (docs/13). It subscribes to event types and
// returns the events it wants published. Handlers must be idempotent: build
// output events with Spec.Key so a re-run yields the same IDs.
type Handler interface {
	Name() string
	Types() []string
	Handle(ctx context.Context, e *Event) ([]*Event, error)
}

// Dispatcher drives handlers from the log. Each handler has its own offset
// (consumer group). Output events and the offset advance are not one
// transaction, so delivery is at-least-once; deterministic IDs make it
// effectively-once.
type Dispatcher struct {
	Store    *Store
	Handlers []Handler
	Log      *slog.Logger
	Interval time.Duration
}

// Run processes events until ctx is cancelled.
func (d *Dispatcher) Run(ctx context.Context) error {
	if d.Interval == 0 {
		d.Interval = 300 * time.Millisecond
	}
	t := time.NewTicker(d.Interval)
	defer t.Stop()
	for {
		progressed, err := d.Step(ctx)
		if err != nil {
			d.Log.Error("dispatch", "err", err)
		}
		if progressed {
			continue // drain without waiting
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

// Step gives every handler one batch. It reports whether any event was handled.
func (d *Dispatcher) Step(ctx context.Context) (bool, error) {
	progressed := false
	for _, h := range d.Handlers {
		n, err := d.stepHandler(ctx, h)
		if err != nil {
			return progressed, fmt.Errorf("%s: %w", h.Name(), err)
		}
		progressed = progressed || n > 0
	}
	return progressed, nil
}

func (d *Dispatcher) stepHandler(ctx context.Context, h Handler) (int, error) {
	off, err := d.Store.Offset(ctx, h.Name())
	if err != nil {
		return 0, err
	}
	evs, err := d.Store.After(ctx, off, h.Types(), 50)
	if err != nil {
		return 0, err
	}
	for _, e := range evs {
		out, err := h.Handle(ctx, e)
		if err != nil {
			// Stop at the failing event; it is retried next step. A real
			// deployment would back off and dead-letter (docs/13 §1 rule 6).
			return 0, fmt.Errorf("handle %s %s: %w", e.Type, e.ID, err)
		}
		if len(out) > 0 {
			if _, err := d.Store.Publish(ctx, out...); err != nil {
				return 0, err
			}
			for _, o := range out {
				d.Log.Info("emit", "handler", h.Name(), "type", o.Type, "subject", o.Subject)
			}
		}
		if err := d.Store.Commit(ctx, h.Name(), e.Seq); err != nil {
			return 0, err
		}
	}
	// Nothing matched: still advance past unrelated events cheaply next time.
	return len(evs), nil
}
