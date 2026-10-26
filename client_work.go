package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
)

// lobbyItem is one row of GET /work (listOpenWork): a graph that advertises
// open seats to the caller. Numbers and states are the server's, unchanged.
type lobbyItem struct {
	GraphID     string          `json:"graph_id"`
	Name        string          `json:"name"`
	Slug        string          `json:"slug"`
	State       string          `json:"state"`
	Priority    int             `json:"priority"`
	WorkersWant int             `json:"workers_wanted"`
	JudgesWant  int             `json:"judges_wanted"`
	OpensNextAt string          `json:"opens_next_at"`
	Claim       string          `json:"claim"`
	MyRole      string          `json:"my_role"`
	Validations string          `json:"validations"`
	Epoch       int             `json:"epoch"`
	Seats       json.RawMessage `json:"seats"`
}

// recruitingInfo is data of getGraphRecruiting.
type recruitingInfo struct {
	GraphID    string `json:"graph_id"`
	Recruiting struct {
		Open        bool     `json:"open"`
		Audience    string   `json:"audience"`
		Accounts    []string `json:"accounts"`
		Groups      []string `json:"groups"`
		MaxParallel int      `json:"max_parallel"`
		Priority    int      `json:"priority"`
	} `json:"recruiting"`
	AudienceWithoutClaim json.RawMessage `json:"audience_without_claim"`
	GroupsUnresolved     bool            `json:"groups_unresolved"`
}

// openWork walks the lobby's nested {items,total,limit,offset} pages. query
// carries the server-side filters (kind, type, skill, group, for).
func (c *client) openWork(ctx context.Context, query url.Values) ([]lobbyItem, error) {
	var all []lobbyItem
	for off := 0; ; off += 200 {
		q := url.Values{}
		for k, v := range query {
			q[k] = v
		}
		q.Set("limit", "200")
		q.Set("offset", strconv.Itoa(off))
		var r struct {
			Data struct {
				Items []lobbyItem
				Total int
			}
		}
		if err := c.do(ctx, http.MethodGet, "/work?"+q.Encode(), nil, &r); err != nil {
			return nil, err
		}
		all = append(all, r.Data.Items...)
		if len(r.Data.Items) < 200 || len(all) >= r.Data.Total {
			return all, nil
		}
	}
}

func (c *client) recruiting(ctx context.Context, graphID string) (recruitingInfo, error) {
	var r struct{ Data recruitingInfo }
	err := c.do(ctx, http.MethodGet, "/graphs/"+url.PathEscape(graphID)+"/recruiting", nil, &r)
	return r.Data, err
}
