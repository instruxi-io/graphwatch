package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// graphSummary is the part of GET /graphs/{id}/summary the picker shows.
type graphSummary struct {
	State    string         `json:"state"`
	ByStatus map[string]int `json:"by_status"`
}

func (c *client) graphSummary(ctx context.Context, id string) (graphSummary, error) {
	var s graphSummary
	d, err := c.graphData(ctx, "/graphs/"+id+"/summary")
	if err != nil {
		return s, err
	}
	err = json.Unmarshal(d, &s)
	return s, err
}

func (s graphSummary) total() int {
	n := 0
	for _, v := range s.ByStatus {
		n += v
	}
	return n
}

// counts is "done 3, active 1", keys sorted, zeros dropped.
func (s graphSummary) counts() string {
	var ks []string
	for k, v := range s.ByStatus {
		if v > 0 {
			ks = append(ks, k)
		}
	}
	sort.Strings(ks)
	parts := make([]string, len(ks))
	for i, k := range ks {
		parts[i] = fmt.Sprintf("%s %d", k, s.ByStatus[k])
	}
	return strings.Join(parts, ", ")
}
