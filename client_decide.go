package main

import (
	"context"
	"net/url"
)

// decideRequest is the body of POST /graphs/{id}/nodes/{nodeId}/decide. Who
// decided and when are not in it: the service takes them from the credential
// and its own clock.
type decideRequest struct {
	Decision  string   `json:"decision"`
	Note      string   `json:"note,omitempty"`
	FollowUps []string `json:"follow_ups,omitempty"`
}

// apiFrontierNode is one node a decision released.
type apiFrontierNode struct {
	NodeID string `json:"node_id"`
	Key    string `json:"key"`
	Title  string `json:"title"`
	Type   string `json:"type"`
}

// decideResponse carries only the frontier; the node's own status is read
// back with a GET, so the viewer states what the service holds.
type decideResponse struct {
	Frontier []apiFrontierNode `json:"frontier"`
}

// decideGate is the one write graphwatch makes outside --demo. It is not
// retried: a decision whose reply was lost has still been recorded, and the
// retry would answer invalid_transition.
func (c *client) decideGate(ctx context.Context, graphID, nodeID string, req decideRequest) (decideResponse, error) {
	var out decideResponse
	err := c.do(ctx, "POST", "/graphs/"+url.PathEscape(graphID)+"/nodes/"+url.PathEscape(nodeID)+"/decide", req, &out)
	return out, err
}
