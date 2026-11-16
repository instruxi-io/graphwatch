package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// apiNodeFull is a node as GET /graphs/{id}/nodes returns it. WorkState is the
// server's field and is only ever displayed, never derived. Assignment fields
// are the same: the server decides who may claim (migration 169) and the
// client prints that, rather than recomputing group membership.
type apiNodeFull struct {
	ID             string          `json:"id"`
	Key            string          `json:"key"`
	Type           string          `json:"type"`
	Title          string          `json:"title"`
	Description    string          `json:"description"`
	Status         string          `json:"status"`
	WorkState      string          `json:"work_state"`
	Assignee       nodeAssignee    `json:"assignee"`
	AssignmentMode string          `json:"assignment_mode"`
	Private        bool            `json:"private"`
	ViewerCanClaim bool            `json:"viewer_can_claim"`
	ClaimBlock     *string         `json:"claim_block"`
	Reclaimable    bool            `json:"reclaimable"`
	OpensAt        *time.Time      `json:"opens_at"`
	Runner         string          `json:"runner"`
	Data           json.RawMessage `json:"data"`
	Raw            json.RawMessage `json:"-"`
}

// nodeAssignee is whose work a node is. The server sends an object
// {account_id, group_slug} (either half may be null). An older server sent a
// plain string; both have to decode or a node list dies on the first assigned
// node (decide included: the decision was never sent).
type nodeAssignee struct {
	AccountID *string `json:"account_id"`
	GroupSlug *string `json:"group_slug"`
	// Name, when a payload carries one, is what a person is called. The
	// contract's assignee object has no name; account_id is the fallback.
	Name string `json:"name"`
	text string // set when the JSON value was a string
}

func (a *nodeAssignee) UnmarshalJSON(b []byte) error {
	b = []byte(strings.TrimSpace(string(b)))
	if len(b) == 0 || string(b) == "null" {
		return nil
	}
	if b[0] == '"' {
		return json.Unmarshal(b, &a.text)
	}
	// A local alias so this method does not call itself.
	type plain nodeAssignee
	var p plain
	if err := json.Unmarshal(b, &p); err != nil {
		// An unexpected shape must not fail the whole node.
		return nil
	}
	*a = nodeAssignee(p)
	return nil
}

// account is the person this node is assigned to: a display name when one was
// sent, otherwise the account id, otherwise a legacy string assignee.
func (a nodeAssignee) account() string {
	if a.Name != "" {
		return a.Name
	}
	if a.AccountID != nil && *a.AccountID != "" {
		return *a.AccountID
	}
	return a.text
}

func (a nodeAssignee) group() string {
	if a.GroupSlug != nil {
		return *a.GroupSlug
	}
	return ""
}

// display is the assignee line: a person, a group, or both. Empty when unset.
func (a nodeAssignee) display() string {
	name, group := a.account(), a.group()
	switch {
	case name != "" && group != "":
		return name + " (" + group + ")"
	case name != "":
		return name
	default:
		return group
	}
}

// who is the name a reserved-node error uses. A group slug counts; a blank
// means the caller has no name to give.
func (a nodeAssignee) who() string {
	if name := a.account(); name != "" {
		return name
	}
	return a.group()
}

// apiNodeRunRow is one row of GET /graphs/{id}/nodes/{nodeId}/runs.
type apiNodeRunRow struct {
	ID        string          `json:"id"`
	Attempt   int             `json:"attempt"`
	Status    string          `json:"status"`
	CreatedAt time.Time       `json:"created_at"`
	Data      json.RawMessage `json:"data"`
}

func decodeNodes(raw []json.RawMessage) ([]apiNodeFull, error) {
	out := make([]apiNodeFull, 0, len(raw))
	for _, r := range raw {
		var n apiNodeFull
		if err := json.Unmarshal(r, &n); err != nil {
			return nil, err
		}
		n.Raw = r
		out = append(out, n)
	}
	return out, nil
}

// nodesFull is read-only.
func (c *client) nodesFull(ctx context.Context, graphID string) ([]apiNodeFull, error) {
	raw, err := pagesParallel[json.RawMessage](ctx, c, "/graphs/"+graphID+"/nodes", 0, nil)
	if err != nil {
		return nil, err
	}
	return decodeNodes(raw)
}

func (c *client) nodeFull(ctx context.Context, graphID, nodeID string) (apiNodeFull, error) {
	var r struct{ Data json.RawMessage }
	if err := c.do(ctx, http.MethodGet, "/graphs/"+graphID+"/nodes/"+nodeID, nil, &r); err != nil {
		return apiNodeFull{}, err
	}
	ns, err := decodeNodes([]json.RawMessage{r.Data})
	if err != nil {
		return apiNodeFull{}, err
	}
	return ns[0], nil
}

func (c *client) nodeRunList(ctx context.Context, graphID, nodeID string) ([]apiNodeRunRow, error) {
	return page[apiNodeRunRow](ctx, c, "/graphs/"+graphID+"/nodes/"+nodeID+"/runs")
}

// claimNode is POST /graphs/{id}/nodes/{nodeId}/claim. The body is the claim
// card's data, raw, so --json can print it unchanged. A 409 node_reserved is
// the same error the frontier claim and StartRun return; the caller prints it.
func (c *client) claimNode(ctx context.Context, graphID, nodeID string) (json.RawMessage, error) {
	var resp struct {
		Data json.RawMessage `json:"data"`
	}
	if err := c.do(ctx, http.MethodPost, "/graphs/"+graphID+"/nodes/"+nodeID+"/claim", map[string]any{}, &resp); err != nil {
		return nil, err
	}
	return resp.Data, nil
}
