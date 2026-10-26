package main

import (
	"context"
	"encoding/json"
	"net/url"
)

// apiRoute is GET /graphs/{id}/nodes/{nodeId}/route. The service's words are
// kept verbatim; the viewer decides nothing from them. RouteUndecided is the
// service saying no route has been chosen yet, which is different from a
// route whose tier is simply unset.
type apiRoute struct {
	Tier           string          `json:"tier"`
	Model          string          `json:"model"`
	Reason         string          `json:"reason"`
	Source         string          `json:"source"`
	RouteUndecided bool            `json:"route_undecided"`
	State          string          `json:"state"`
	Raw            json.RawMessage `json:"-"`
}

// nodeRoute is read-only.
func (c *client) nodeRoute(ctx context.Context, graphID, nodeID string) (apiRoute, error) {
	var env struct {
		Data apiRoute `json:"data"`
	}
	err := c.do(ctx, "GET", "/graphs/"+url.PathEscape(graphID)+"/nodes/"+url.PathEscape(nodeID)+"/route", nil, &env)
	return env.Data, err
}
