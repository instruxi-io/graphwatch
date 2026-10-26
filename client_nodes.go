package main

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// apiNodeFull is a node as GET /graphs/{id}/nodes returns it. WorkState is the
// server's field and is only ever displayed, never derived.
type apiNodeFull struct {
	ID          string          `json:"id"`
	Key         string          `json:"key"`
	Type        string          `json:"type"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
	Status      string          `json:"status"`
	WorkState   string          `json:"work_state"`
	Assignee    string          `json:"assignee"`
	Reclaimable bool            `json:"reclaimable"`
	OpensAt     *time.Time      `json:"opens_at"`
	Runner      string          `json:"runner"`
	Data        json.RawMessage `json:"data"`
	Raw         json.RawMessage `json:"-"`
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
	raw, err := page[json.RawMessage](ctx, c, "/graphs/"+graphID+"/nodes")
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
