package session

import (
	"github.com/notbobutah/agentic-pipeline/internal/event"
	"github.com/notbobutah/agentic-pipeline/internal/work"
)

// Event types owned or consumed by sessions.
const (
	TRequested     = "ap.devsession.requested.v1"
	TStarted       = "ap.devsession.started.v1"
	TCheckpoint    = "ap.devsession.checkpoint.v1"
	TInnerLoop     = "ap.devsession.inner_loop.completed.v1"
	TCommitPushed  = "ap.devsession.commit.pushed.v1"
	TChangeOpened  = "ap.devsession.change.opened.v1"
	TFeedback      = "ap.devsession.feedback.received.v1"
	TIteration     = "ap.devsession.iteration.started.v1"
	TReady         = "ap.devsession.ready_for_review.v1"
	TBlocked       = "ap.devsession.blocked.v1"
	TCompleted     = "ap.devsession.completed.v1"
	TChangesetMade = "ap.changeset.created.v1"
	TCSUpdated     = "ap.changeset.member.updated.v1"
	TCSReady       = "ap.changeset.ready.v1"
	TCSMerged      = "ap.changeset.merged.v1"
	TJobCompleted  = "ap.pipeline.job.completed.v1"
	TRunCompleted  = "ap.pipeline.run.completed.v1"
	TDiagnosis     = "ap.diagnosis.completed.v1"
	TReview        = "ap.change.review.submitted.v1"
	TMerged        = "ap.change.merged.v1"
	TDeployed      = "ap.deployment.completed.v1"
	TWorkCompleted = "ap.work.completed.v1"
)

// ---- payloads ----

type Started struct {
	SessionID string `json:"sessionId"`
	Component string `json:"component"`
	Repo      string `json:"repo"`
	Branch    string `json:"branch"`
	PinSHA    string `json:"pinSha,omitempty"`
}

type Checkpoint struct {
	SessionID string `json:"sessionId"`
	Component string `json:"component"`
	Phase     string `json:"phase"`
	Summary   string `json:"summary"`
}

type InnerLoop struct {
	SessionID string   `json:"sessionId"`
	Component string   `json:"component"`
	Role      string   `json:"role"`
	Commands  []string `json:"commands"`
	Passing   bool     `json:"allPassing"`
}

type CommitPushed struct {
	SessionID  string   `json:"sessionId"`
	Component  string   `json:"component"`
	Repo       string   `json:"repo"`
	Branch     string   `json:"branch"`
	SHA        string   `json:"sha"`
	Kind       string   `json:"kind"` // tests | implementation | fix | repin
	Files      []string `json:"files"`
	Message    string   `json:"message"`
	ProposalID string   `json:"proposalId,omitempty"`
	Iteration  int      `json:"iteration"`
	TestFiles  []string `json:"testFiles,omitempty"`
}

type ChangeOpened struct {
	SessionID    string `json:"sessionId"`
	Component    string `json:"component"`
	Repo         string `json:"repo"`
	ChangeNumber int    `json:"changeNumber"`
	URL          string `json:"url"`
	Draft        bool   `json:"draft"`
}

type Feedback struct {
	SessionID string `json:"sessionId"`
	Component string `json:"component"`
	Source    string `json:"source"` // pipeline | review
	HeadSHA   string `json:"headSha"`
	Summary   string `json:"summary"`
	Details   string `json:"details"`
	Trusted   bool   `json:"trusted"`
}

type Iteration struct {
	SessionID string `json:"sessionId"`
	Component string `json:"component"`
	Iteration int    `json:"iteration"`
	Trigger   string `json:"trigger"`
}

type Ready struct {
	SessionID    string `json:"sessionId"`
	Component    string `json:"component"`
	Repo         string `json:"repo"`
	ChangeNumber int    `json:"changeNumber"`
	URL          string `json:"url"`
}

type Blocked struct {
	SessionID string `json:"sessionId"`
	Component string `json:"component"`
	Reason    string `json:"reason"`
	Detail    string `json:"detail"`
}

type Job struct {
	Component    string `json:"component"`
	Repo         string `json:"repo"`
	ChangeNumber int    `json:"changeNumber"`
	HeadSHA      string `json:"headSha"`
	Check        string `json:"check"`
	Conclusion   string `json:"conclusion"`
	URL          string `json:"url"`
	JobID        int64  `json:"jobId"`
}

type Run struct {
	Component    string   `json:"component"`
	Repo         string   `json:"repo"`
	ChangeNumber int      `json:"changeNumber"`
	HeadSHA      string   `json:"headSha"`
	Conclusion   string   `json:"conclusion"`
	Checks       []string `json:"checks"`
	Failed       []string `json:"failed,omitempty"`
}

type Diagnosis struct {
	Component    string   `json:"component"`
	HeadSHA      string   `json:"headSha"`
	Class        string   `json:"class"`
	Method       string   `json:"method"`
	Summary      string   `json:"summary"`
	FailedChecks []string `json:"failedChecks"`
	FailedSteps  []string `json:"failedSteps"`
}

type Review struct {
	Component    string `json:"component"`
	Repo         string `json:"repo"`
	ChangeNumber int    `json:"changeNumber"`
	Reviewer     string `json:"reviewer"`
	State        string `json:"state"` // approved | changes_requested | commented
	Body         string `json:"body"`
	ReviewID     int64  `json:"reviewId"`
}

type Merged struct {
	Component    string `json:"component"`
	Repo         string `json:"repo"`
	ChangeNumber int    `json:"changeNumber"`
	MergeSHA     string `json:"mergeSha"`
	MergedBy     string `json:"mergedBy"`
	URL          string `json:"url"`
}

type Deployed struct {
	Component   string `json:"component"`
	Repo        string `json:"repo"`
	SHA         string `json:"sha"`
	Environment string `json:"environment"`
	Conclusion  string `json:"conclusion"`
	URL         string `json:"url"`
}

// ---- projection ----

// Member is one component's session within a work item.
type Member struct {
	Component    string
	Repo         string
	Requested    *event.Event
	Started      bool
	Branch       string
	PinSHA       string
	HeadSHA      string
	TestFiles    []string
	TestsDone    bool
	ImplDone     bool
	Iterations   int
	ChangeNumber int
	ChangeURL    string
	Opened       *event.Event
	RunHead      string // head SHA of the last completed CI run
	RunOK        bool
	Ready        bool
	Merged       bool
	MergeSHA     string
	Blocked      string
	PathGlobs    []string
}

// Item is the session view of a work item.
type Item struct {
	Work      *work.State
	Members   map[string]*Member
	Changeset []string // member components, producer first
	CSReady   bool
	CSMerged  bool
	Deployed  bool
	Done      bool
}

// Fold builds the session view from a correlation's events.
func Fold(evs []*event.Event) (*Item, error) {
	ws, err := work.Replay(evs)
	if err != nil {
		return nil, err
	}
	it := &Item{Work: ws, Members: map[string]*Member{}}
	m := func(c string) *Member {
		if it.Members[c] == nil {
			it.Members[c] = &Member{Component: c}
		}
		return it.Members[c]
	}
	for _, e := range evs {
		switch e.Type {
		case TRequested:
			var d work.SessionRequested
			_ = e.Decode(&d)
			x := m(d.Component)
			x.Repo, x.Requested, x.PathGlobs = d.Repo, e, d.PathGlobs
		case TChangesetMade:
			var d work.ChangesetCreated
			_ = e.Decode(&d)
			it.Changeset = d.MergeOrder
		case TStarted:
			var d Started
			_ = e.Decode(&d)
			x := m(d.Component)
			x.Started, x.Branch, x.PinSHA = true, d.Branch, d.PinSHA
		case TCommitPushed:
			var d CommitPushed
			_ = e.Decode(&d)
			x := m(d.Component)
			x.HeadSHA = d.SHA
			switch d.Kind {
			case "tests":
				x.TestsDone, x.TestFiles = true, d.TestFiles
			case "implementation":
				x.ImplDone = true
			case "fix":
				x.Iterations = max(x.Iterations, d.Iteration)
			}
		case TChangeOpened:
			var d ChangeOpened
			_ = e.Decode(&d)
			x := m(d.Component)
			x.ChangeNumber, x.ChangeURL, x.Opened = d.ChangeNumber, d.URL, e
		case TRunCompleted:
			var d Run
			_ = e.Decode(&d)
			x := m(d.Component)
			x.RunHead, x.RunOK = d.HeadSHA, d.Conclusion == "success"
		case TIteration:
			var d Iteration
			_ = e.Decode(&d)
			x := m(d.Component)
			x.Iterations = max(x.Iterations, d.Iteration)
		case TReady:
			var d Ready
			_ = e.Decode(&d)
			m(d.Component).Ready = true
		case TBlocked:
			var d Blocked
			_ = e.Decode(&d)
			m(d.Component).Blocked = d.Reason + ": " + d.Detail
		case TMerged:
			var d Merged
			_ = e.Decode(&d)
			x := m(d.Component)
			x.Merged, x.MergeSHA = true, d.MergeSHA
		case TCSReady:
			it.CSReady = true
		case TCSMerged:
			it.CSMerged = true
		case TDeployed:
			it.Deployed = true
		case TWorkCompleted:
			it.Done = true
		}
	}
	if len(it.Changeset) == 0 {
		for c := range it.Members {
			it.Changeset = append(it.Changeset, c)
		}
	}
	return it, nil
}

// Green reports whether the member's current head has a successful CI run.
func (m *Member) Green() bool { return m.HeadSHA != "" && m.RunHead == m.HeadSHA && m.RunOK }
