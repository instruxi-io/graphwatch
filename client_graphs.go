package main

import (
	"context"
	"encoding/json"
	"net/http"
)

// apiGraphRow is a graph as GET /graphs returns it. The list does not carry the
// roll-up today; State is shown only when a server version adds it. ArchivedAt
// is read tolerantly: the schema has no archive field on a graph yet.
type apiGraphRow struct {
	ID         string          `json:"id"`
	Slug       string          `json:"slug"`
	Name       string          `json:"name"`
	Mode       string          `json:"mode"`
	Lifecycle  string          `json:"lifecycle"`
	State      string          `json:"state"`
	ArchivedAt string          `json:"archived_at"`
	Raw        json.RawMessage `json:"-"`
}

// graphsList walks GET /graphs. Read-only.
func (c *client) graphsList(ctx context.Context) ([]apiGraphRow, error) {
	raw, err := page[json.RawMessage](ctx, c, "/graphs")
	if err != nil {
		return nil, err
	}
	out := make([]apiGraphRow, 0, len(raw))
	for _, r := range raw {
		var g apiGraphRow
		if err := json.Unmarshal(r, &g); err != nil {
			return nil, err
		}
		g.Raw = r
		out = append(out, g)
	}
	return out, nil
}

// graphData GETs path and returns the envelope's data payload verbatim.
func (c *client) graphData(ctx context.Context, path string) (json.RawMessage, error) {
	var r struct{ Data json.RawMessage }
	err := c.do(ctx, http.MethodGet, path, nil, &r)
	return r.Data, err
}
