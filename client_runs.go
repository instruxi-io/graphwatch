package main

import (
	"context"
	"encoding/json"
	"net/url"
	"time"
)

// apiEvidence is one evidence entry on a run. Kind is kept verbatim.
type apiEvidence struct {
	Kind    string `json:"kind"`
	Cmd     string `json:"cmd"`
	Exit    *int   `json:"exit"`
	Output  string `json:"output"`
	Path    string `json:"path"`
	Excerpt string `json:"excerpt"`
	Label   string `json:"label"`
}

type apiCriterion struct {
	Text string   `json:"text"`
	Noul *float64 `json:"noul"` // P(met), one per acceptance line in order
}

// apiVerification is a run's verification (and a verdict row's). State, policy
// and reason are the service's vocabulary and are printed verbatim.
type apiVerification struct {
	State         string          `json:"state"`
	Policy        string          `json:"policy"`
	Reason        string          `json:"reason"`
	Confidence    *float64        `json:"confidence"`
	Model         string          `json:"model"`
	Criteria      []apiCriterion  `json:"criteria"`
	Checks        json.RawMessage `json:"checks"`
	PendingHuman  bool            `json:"pending_human"`
	LowConfidence bool            `json:"low_confidence"`
	DecidedBy     string          `json:"decided_by"`
	Escalated     string          `json:"escalated"`
}

type apiTally struct {
	Accept    int `json:"accept"`
	Reject    int `json:"reject"`
	Undecided int `json:"undecided"`
}

// apiValidation is the run's validation-v1 snapshot: state is
// pending | accepted | rejected | escalated.
type apiValidation struct {
	State     string          `json:"state"`
	Reason    string          `json:"reason"`
	Tally     *apiTally       `json:"tally"`
	Assigned  []string        `json:"assigned"`
	Config    json.RawMessage `json:"config"`
	DecidedBy string          `json:"decided_by"`
}

type apiRun struct {
	ID           string           `json:"id"`
	NodeID       string           `json:"node_id"`
	Attempt      int              `json:"attempt"`
	Status       string           `json:"status"`
	Runner       string           `json:"runner"`
	Client       string           `json:"client"`
	StartedAt    time.Time        `json:"started_at"`
	EndedAt      *time.Time       `json:"ended_at"`
	Error        string           `json:"error"`
	Report       string           `json:"report"`
	Outputs      map[string]any   `json:"outputs"`
	Evidence     []apiEvidence    `json:"evidence"`
	Verification *apiVerification `json:"verification"`
	Validation   *apiValidation   `json:"validation"`
	Raw          json.RawMessage  `json:"-"`
}

type apiVerdict struct {
	ID           string          `json:"id"`
	State        string          `json:"state"`
	Model        string          `json:"model"`
	NodeMoved    bool            `json:"node_moved"`
	CreatedAt    time.Time       `json:"created_at"`
	Verification apiVerification `json:"verification"`
}

type apiVote struct {
	ID        string    `json:"id"`
	Voter     string    `json:"voter"`
	Vote      string    `json:"vote"`
	Note      string    `json:"note"`
	CreatedAt time.Time `json:"created_at"`
}

func runPath(graphID, nodeID string) string {
	return "/graphs/" + url.PathEscape(graphID) + "/nodes/" + url.PathEscape(nodeID) + "/runs"
}

// Every call here is a GET: complete, heartbeat, cancel-request and vote are
// never reachable from here.
func (c *client) nodeRuns(ctx context.Context, graphID, nodeID string) ([]apiRun, []json.RawMessage, error) {
	raw, err := page[json.RawMessage](ctx, c, runPath(graphID, nodeID))
	if err != nil {
		return nil, nil, err
	}
	runs := make([]apiRun, 0, len(raw))
	for _, r := range raw {
		var run apiRun
		if err := json.Unmarshal(r, &run); err != nil {
			return nil, nil, err
		}
		runs = append(runs, run)
	}
	return runs, raw, nil
}

func (c *client) runVerdicts(ctx context.Context, graphID, nodeID, runID string) ([]apiVerdict, error) {
	return page[apiVerdict](ctx, c, runPath(graphID, nodeID)+"/"+url.PathEscape(runID)+"/verdicts")
}

func (c *client) runVotes(ctx context.Context, graphID, nodeID, runID string) ([]apiVote, error) {
	return page[apiVote](ctx, c, runPath(graphID, nodeID)+"/"+url.PathEscape(runID)+"/votes")
}
