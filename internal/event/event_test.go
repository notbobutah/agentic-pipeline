package event

import (
	"context"
	"errors"
	"path/filepath"
	"regexp"
	"testing"
)

func testStore(t *testing.T, types ...string) *Store {
	t.Helper()
	c := &Catalog{types: map[string]bool{}}
	for _, ty := range types {
		c.types[ty] = true
	}
	s, err := Open(filepath.Join(t.TempDir(), "events.db"), c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestPublishIsIdempotentForKeyedEvents(t *testing.T) {
	ctx := context.Background()
	s := testStore(t, "ap.work.item.assigned.v1", "ap.work.request.received.v1")
	root, _ := New(Spec{Type: "ap.work.item.assigned.v1", Subject: "work/DEMO-1", CorrelationID: "wi-demo-DEMO-1", Key: "delivery-1"})
	again, _ := New(Spec{Type: "ap.work.item.assigned.v1", Subject: "work/DEMO-1", CorrelationID: "wi-demo-DEMO-1", Key: "delivery-1"})
	if root.ID != again.ID {
		t.Fatalf("keyed ids differ: %s vs %s", root.ID, again.ID)
	}
	if n, err := s.Publish(ctx, root); err != nil || n != 1 {
		t.Fatalf("first publish: n=%d err=%v", n, err)
	}
	if n, err := s.Publish(ctx, again); err != nil || n != 0 {
		t.Fatalf("duplicate publish: n=%d err=%v", n, err)
	}
	child, _ := New(Spec{Type: "ap.work.request.received.v1", Subject: "work/DEMO-1", Cause: root, Key: "x"})
	if child.CorrelationID != root.CorrelationID || child.CausationID != root.ID {
		t.Fatalf("child did not inherit correlation/causation: %+v", child)
	}
	if child.TraceParent[:36] != root.TraceParent[:36] {
		t.Fatalf("child trace id %s not in parent trace %s", child.TraceParent, root.TraceParent)
	}
	evs, _ := s.Correlation(ctx, "wi-demo-DEMO-1")
	if len(evs) != 1 {
		t.Fatalf("want 1 event, got %d", len(evs))
	}
}

func TestCatalogGateRejectsUnknownTypes(t *testing.T) {
	s := testStore(t, "ap.work.item.assigned.v1")
	e, _ := New(Spec{Type: "ap.made.up.v1", Subject: "x", CorrelationID: "c"})
	if _, err := s.Publish(context.Background(), e); !errors.Is(err, ErrUncatalogued) {
		t.Fatalf("want ErrUncatalogued, got %v", err)
	}
}

func TestEnvelopeShape(t *testing.T) {
	e, err := New(Spec{Type: "ap.work.plan.approved.v1", Subject: "plan/p1", CorrelationID: "wi-demo-DEMO-1"})
	if err != nil {
		t.Fatal(err)
	}
	if e.DataSchema != "ap://schemas/ap.work.plan.approved/1.0.0" {
		t.Errorf("dataschema = %s", e.DataSchema)
	}
	if !regexp.MustCompile(`^[0-9a-f]{2}-[0-9a-f]{32}-[0-9a-f]{16}-[0-9a-f]{2}$`).MatchString(e.TraceParent) {
		t.Errorf("traceparent = %s", e.TraceParent)
	}
	if e.Repo != "_platform" || e.PartitionKey != e.CorrelationID {
		t.Errorf("platform event partitioning: repo=%s pk=%s", e.Repo, e.PartitionKey)
	}
}
