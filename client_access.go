package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
)

type apiAccessSource struct {
	Kind      string `json:"kind"`
	GroupSlug string `json:"group_slug"`
}

type apiAccessAccount struct {
	AccountID string            `json:"account_id"`
	Username  string            `json:"username"`
	Email     string            `json:"email"`
	Role      string            `json:"role"`
	Via       []apiAccessSource `json:"via"`
}

type apiAccess struct {
	Accounts         []apiAccessAccount `json:"accounts"`
	GroupsUnresolved bool               `json:"groups_unresolved"`
}

type apiEpochStats struct {
	Nodes  int `json:"nodes"`
	Done   int `json:"done"`
	Failed int `json:"failed"`
}

type apiEpoch struct {
	Epoch       int           `json:"epoch"`
	Current     bool          `json:"current"`
	StartedAt   string        `json:"started_at"`
	EndedAt     *string       `json:"ended_at"`
	StartReason string        `json:"start_reason"`
	Stats       apiEpochStats `json:"stats"`
}

// access reads GET /graphs/{id}/access; the raw payload is kept for --json.
func (c *client) access(ctx context.Context, id string) (apiAccess, json.RawMessage, error) {
	var r struct{ Data json.RawMessage }
	var a apiAccess
	if err := c.do(ctx, http.MethodGet, "/graphs/"+url.PathEscape(id)+"/access", nil, &r); err != nil {
		return a, nil, err
	}
	err := json.Unmarshal(r.Data, &a)
	return a, r.Data, err
}

func (c *client) epochs(ctx context.Context, id string) ([]apiEpoch, error) {
	return page[apiEpoch](ctx, c, "/graphs/"+url.PathEscape(id)+"/epochs")
}

func (c *client) epoch(ctx context.Context, id string, n int) (apiEpoch, error) {
	var r struct{ Data apiEpoch }
	err := c.do(ctx, http.MethodGet, "/graphs/"+url.PathEscape(id)+"/epochs/"+strconv.Itoa(n), nil, &r)
	return r.Data, err
}
