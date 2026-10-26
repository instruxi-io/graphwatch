package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
)

// queryPage is the data payload of /nodes/query and /runs/query: items when
// ungrouped, groups when group_by is set. Next is the keyset cursor, passed
// back unchanged as `after`.
type queryPage struct {
	Items  []json.RawMessage `json:"items"`
	Groups []queryGroup      `json:"groups"`
	Next   string            `json:"next"`
	Total  int               `json:"total"`
}

type queryGroup struct {
	Key   map[string]any `json:"key"`
	Count int            `json:"count"`
	Aggs  map[string]any `json:"aggs"`
}

// query is one read of kind ("nodes" or "runs"). Only GET is ever sent.
func (c *client) query(ctx context.Context, kind string, params url.Values) (queryPage, json.RawMessage, error) {
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := c.do(ctx, http.MethodGet, "/"+kind+"/query?"+params.Encode(), nil, &env); err != nil {
		return queryPage{}, nil, err
	}
	var p queryPage
	if err := json.Unmarshal(env.Data, &p); err != nil {
		return queryPage{}, nil, err
	}
	return p, env.Data, nil
}
