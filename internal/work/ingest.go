package work

import (
	"context"
	"fmt"
	"time"

	"github.com/notbobutah/agentic-pipeline/internal/config"
	"github.com/notbobutah/agentic-pipeline/internal/event"
)

// The local card adapter stands in for the Jira webhook mapping (docs/05 §3):
// it emits exactly the L1 events a real Jira integration would.

func l1(cfg *config.Config, key, typ, deliveryKey string, actor string, data any) (*event.Event, error) {
	return l1From(cfg, "card", key, typ, deliveryKey, actor, data)
}

func l1From(cfg *config.Config, provider, key, typ, deliveryKey string, actor string, data any) (*event.Event, error) {
	return event.New(event.Spec{
		Type: typ, Subject: Subject(key), Source: "//tracker/" + provider, Origin: "webhook",
		CorrelationID: CorrelationID(cfg.Tenant, key), Key: deliveryKey, Data: data,
		Actor: &event.Actor{Kind: "human", ID: "tracker:" + actor, Login: actor},
	})
}

// SubmitCard publishes the card's assignment and its guidance comments.
// Re-submitting the same card is a no-op (deterministic delivery keys).
func SubmitCard(ctx context.Context, st *event.Store, cfg *config.Config, c *Card) (int, error) {
	assigned, err := l1(cfg, c.Key, TItemAssigned, c.Key+"/assigned", c.Sponsor, Assigned{
		Card: *c, AssignedBy: c.Sponsor, ToAgent: c.Assignee == cfg.AgentUser,
		Provider: map[string]string{"name": "card", "id": c.Key},
	})
	if err != nil {
		return 0, err
	}
	evs := []*event.Event{assigned}
	for i, g := range c.Guidance {
		id := fmt.Sprintf("%s-g%d", c.Key, i+1)
		e, err := l1(cfg, c.Key, TItemCommented, c.Key+"/"+id, c.Sponsor, Commented{Key: c.Key, CommentID: id, Author: c.Sponsor, Body: g})
		if err != nil {
			return 0, err
		}
		evs = append(evs, e)
	}
	return st.Publish(ctx, evs...)
}

// SubmitIssue publishes a tracker issue's assignment to the agent. The
// delivery key is the issue key, so an issue already submitted is a no-op.
func SubmitIssue(ctx context.Context, st *event.Store, cfg *config.Config, c *Card, provider map[string]string) (int, error) {
	e, err := l1From(cfg, provider["name"], c.Key, TItemAssigned, c.Key+"/assigned", c.Sponsor, Assigned{
		Card: *c, AssignedBy: c.Sponsor, ToAgent: true, Provider: provider,
	})
	if err != nil {
		return 0, err
	}
	return st.Publish(ctx, e)
}

// PublishComment publishes a tracker comment with the tracker's own comment
// ID, so the same comment seen twice is published once.
func PublishComment(ctx context.Context, st *event.Store, cfg *config.Config, key, commentID, author, body, provider string) (int, error) {
	e, err := l1From(cfg, provider, key, TItemCommented, key+"/"+commentID, author, Commented{Key: key, CommentID: commentID, Author: author, Body: body})
	if err != nil {
		return 0, err
	}
	return st.Publish(ctx, e)
}

// Comment publishes a tracker comment (guidance or an /ap command).
func Comment(ctx context.Context, st *event.Store, cfg *config.Config, key, author, body string) error {
	state, err := Load(ctx, st, cfg.Tenant, key)
	if err != nil {
		return err
	}
	if state.Key == "" {
		return fmt.Errorf("no work item %s (submit the card first)", key)
	}
	id := fmt.Sprintf("%s-c%d", key, time.Now().UnixNano())
	e, err := l1(cfg, key, TItemCommented, key+"/"+id, author, Commented{Key: key, CommentID: id, Author: author, Body: body})
	if err != nil {
		return err
	}
	_, err = st.Publish(ctx, e)
	return err
}
