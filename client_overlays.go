package main

import (
	"context"
	"encoding/json"
)

// workStateCounts is by_work_state of GET /graphs/{id}/summary, verbatim.
func (c *client) workStateCounts(ctx context.Context, id string) (map[string]int, error) {
	d, err := c.graphData(ctx, "/graphs/"+id+"/summary")
	if err != nil {
		return nil, err
	}
	var s struct {
		ByWorkState map[string]int `json:"by_work_state"`
	}
	if err := json.Unmarshal(d, &s); err != nil {
		return nil, err
	}
	return s.ByWorkState, nil
}
