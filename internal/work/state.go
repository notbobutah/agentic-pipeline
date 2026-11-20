package work

import (
	"context"
	"time"

	"github.com/notbobutah/agentic-pipeline/internal/event"
)

// Event type names used by the work plane.
const (
	TItemAssigned       = "ap.work.item.assigned.v1"
	TItemCommented      = "ap.work.item.commented.v1"
	TRequestReceived    = "ap.work.request.received.v1"
	TRequestAccepted    = "ap.work.request.accepted.v1"
	TRequestRejected    = "ap.work.request.rejected.v1"
	TGuidance           = "ap.work.guidance.provided.v1"
	TReposResolved      = "ap.work.repos.resolved.v1"
	TPlanRequested      = "ap.work.plan.requested.v1"
	TPlanProposed       = "ap.work.plan.proposed.v1"
	TPlanRevision       = "ap.work.plan.revision.requested.v1"
	TPlanApproved       = "ap.work.plan.approved.v1"
	TPlanRejected       = "ap.work.plan.rejected.v1"
	TWorkBlocked        = "ap.work.blocked.v1"
	TWorkCancelled      = "ap.work.cancelled.v1"
	TCommandReceived    = "ap.command.received.v1"
	TCommandAccepted    = "ap.command.accepted.v1"
	TCommandRejected    = "ap.command.rejected.v1"
	TAgentTaskAssigned  = "ap.agent.task.assigned.v1"
	TAgentTaskCompleted = "ap.agent.task.completed.v1"
	TAgentProposal      = "ap.agent.proposal.created.v1"
	TAgentEscalated     = "ap.agent.escalated.v1"
	TPolicyDecision     = "ap.policy.decision.recorded.v1"
	TSessionRequested   = "ap.devsession.requested.v1"
	TChangesetCreated   = "ap.changeset.created.v1"
)

// ---- Event payloads ----

type Assigned struct {
	Card
	AssignedBy string            `json:"assignedBy"`
	ToAgent    bool              `json:"toAgent"`
	Provider   map[string]string `json:"provider"`
}

type Commented struct {
	Key       string `json:"key"`
	CommentID string `json:"commentId"`
	Author    string `json:"author"`
	Body      string `json:"body"`
}

type RequestReceived struct {
	Key     string `json:"key"`
	Trigger string `json:"trigger"`
	Sponsor string `json:"sponsor"`
}

type Reason struct {
	Reason string `json:"reason"`
	Detail string `json:"detail,omitempty"`
}

type Guidance struct {
	WorkItemID string `json:"workItemId"`
	Author     string `json:"author"`
	AuthorRole string `json:"authorRole"`
	Channel    string `json:"channel"`
	Content    string `json:"content"`
	Scope      string `json:"scope"`
}

type ResolvedRepo struct {
	Component  string  `json:"component"`
	Repo       string  `json:"repo"`
	Confidence float64 `json:"confidence"`
	Source     string  `json:"source"`
}

type ReposResolved struct {
	Repos []ResolvedRepo `json:"repos"`
}

type PlanRequested struct {
	WorkItemID string `json:"workItemId"`
	Version    int    `json:"version"`
	Feedback   string `json:"revisionFeedback,omitempty"`
}

type PlanProposed struct {
	PlanID  string   `json:"planId"`
	Version int      `json:"version"`
	Digest  string   `json:"digest"`
	Plan    Plan     `json:"plan"`
	TaskID  string   `json:"taskId"`
	CostUSD float64  `json:"costUsd"`
	Notes   []string `json:"validationNotes,omitempty"`
}

type PlanRevision struct {
	Version     int    `json:"version"`
	RequestedBy string `json:"requestedBy"`
	Feedback    string `json:"feedback"`
}

type PlanApproved struct {
	PlanID   string `json:"planId"`
	Version  int    `json:"version"`
	Digest   string `json:"digest"`
	Approver string `json:"approver"`
	Channel  string `json:"channel"`
}

type Command struct {
	CommandID string `json:"commandId"`
	Command   string `json:"command"`
	Args      string `json:"args,omitempty"`
	Issuer    string `json:"issuer"`
	Channel   string `json:"channel"`
}

type CommandOutcome struct {
	CommandID string `json:"commandId"`
	Command   string `json:"command"`
	Reason    string `json:"reason,omitempty"`
}

type AgentTask struct {
	TaskID       string  `json:"taskId"`
	Agent        string  `json:"agent"`
	Model        string  `json:"model"`
	MaxTurns     int     `json:"maxTurns"`
	MaxBudgetUSD float64 `json:"maxBudgetUsd"`
}

type AgentTaskCompleted struct {
	TaskID     string   `json:"taskId"`
	Conclusion string   `json:"conclusion"` // success | failure
	Subtype    string   `json:"subtype"`
	CostUSD    float64  `json:"costUsd"`
	Turns      int      `json:"turns"`
	DurationMs int64    `json:"durationMs"`
	Errors     []string `json:"errors,omitempty"`
}

type Proposal struct {
	ProposalID string `json:"proposalId"`
	TaskID     string `json:"taskId"`
	Kind       string `json:"kind"`
	Digest     string `json:"digest"`
}

type Escalated struct {
	TaskID  string `json:"taskId,omitempty"`
	Reason  string `json:"reason"`
	Summary string `json:"summary"`
}

type PolicyDecision struct {
	Query   string   `json:"query"`
	Result  string   `json:"result"` // allow | deny
	Reasons []string `json:"reasons,omitempty"`
}

type SessionRequested struct {
	WorkItemID string   `json:"workItemId"`
	PlanID     string   `json:"planId"`
	Version    int      `json:"version"`
	Component  string   `json:"component"`
	Repo       string   `json:"repo"`
	PathGlobs  []string `json:"pathGlobs"`
}

type ChangesetCreated struct {
	ChangesetID string   `json:"changesetId"`
	WorkItemID  string   `json:"workItemId"`
	Members     []string `json:"members"`
	MergeOrder  []string `json:"mergeOrder"`
}

// ---- Projection ----

// Status of a work item (docs/14 §3).
type Status string

const (
	StReceived     Status = "received"
	StRejected     Status = "rejected"
	StPlanning     Status = "planning"
	StPlanReview   Status = "plan-review"
	StImplementing Status = "implementing"
	StBlocked      Status = "blocked"
	StCancelled    Status = "cancelled"
)

// State is a work item rebuilt from its events. It is the durable-workflow
// substitute: every handler and CLI command derives state by replay.
type State struct {
	Key       string
	Card      Card
	Status    Status
	Repos     []ResolvedRepo
	Guidance  []Guidance
	Plans     []PlanProposed // by version, ascending
	Revisions []PlanRevision
	Approved  *PlanApproved
	Requested int // highest plan version requested
	Reason    string
	CostUSD   float64
	Events    []*event.Event
	Updated   time.Time
}

// Latest returns the newest proposed plan, if any.
func (s *State) Latest() *PlanProposed {
	if len(s.Plans) == 0 {
		return nil
	}
	return &s.Plans[len(s.Plans)-1]
}

// Plan returns a specific version.
func (s *State) Plan(version int) *PlanProposed {
	for i := range s.Plans {
		if s.Plans[i].Version == version {
			return &s.Plans[i]
		}
	}
	return nil
}

// Replay folds a work item's events into State.
func Replay(evs []*event.Event) (*State, error) {
	s := &State{}
	for _, e := range evs {
		s.Events = append(s.Events, e)
		s.Updated = e.Time
		switch e.Type {
		case TItemAssigned:
			var d Assigned
			if err := e.Decode(&d); err != nil {
				return nil, err
			}
			s.Key, s.Card, s.Status = d.Key, d.Card, StReceived
		case TRequestRejected:
			var d Reason
			_ = e.Decode(&d)
			s.Status, s.Reason = StRejected, d.Reason+": "+d.Detail
		case TReposResolved:
			var d ReposResolved
			_ = e.Decode(&d)
			s.Repos = d.Repos
		case TGuidance:
			var d Guidance
			_ = e.Decode(&d)
			s.Guidance = append(s.Guidance, d)
		case TPlanRequested:
			var d PlanRequested
			_ = e.Decode(&d)
			if d.Version > s.Requested {
				s.Requested = d.Version
			}
			s.Status = StPlanning
		case TPlanProposed:
			var d PlanProposed
			if err := e.Decode(&d); err != nil {
				return nil, err
			}
			s.Plans = append(s.Plans, d)
			s.Status = StPlanReview
		case TPlanRevision:
			var d PlanRevision
			_ = e.Decode(&d)
			s.Revisions = append(s.Revisions, d)
			s.Status = StPlanning
		case TPlanApproved:
			var d PlanApproved
			_ = e.Decode(&d)
			s.Approved, s.Status = &d, StImplementing
		case TPlanRejected:
			s.Status = StCancelled
		case TWorkBlocked:
			var d Reason
			_ = e.Decode(&d)
			s.Status, s.Reason = StBlocked, d.Reason+": "+d.Detail
		case TWorkCancelled:
			var d Reason
			_ = e.Decode(&d)
			s.Status, s.Reason = StCancelled, d.Reason
		case TAgentTaskCompleted:
			var d AgentTaskCompleted
			_ = e.Decode(&d)
			s.CostUSD += d.CostUSD
		}
	}
	return s, nil
}

// Load rebuilds a work item's state from the store.
func Load(ctx context.Context, st *event.Store, tenant, key string) (*State, error) {
	evs, err := st.Correlation(ctx, CorrelationID(tenant, key))
	if err != nil {
		return nil, err
	}
	return Replay(evs)
}
