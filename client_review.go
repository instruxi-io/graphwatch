package main

import (
	"context"
	"encoding/json"
	"net/url"
	"time"
)

// apiReviewItem is one human review item (GET /graphs/{id}/review). Kind and
// State are kept verbatim: the service owns their vocabulary.
type apiReviewItem struct {
	ID        string          `json:"id"`
	Kind      string          `json:"kind"`
	NodeID    string          `json:"node_id"`
	NodeKey   string          `json:"node_key"`
	Verdict   *apiVerdictRef  `json:"verdict"`
	RunID     string          `json:"run_id"`
	State     string          `json:"state"`
	Reason    string          `json:"reason"`
	CreatedAt time.Time       `json:"created_at"`
	Raw       json.RawMessage `json:"-"`
}

// apiVerdictRef is the part of an item's verdict the inbox shows: the
// criteria with the judge's probability for each, and the vote tally when the
// run is in validation.
type apiVerdictRef struct {
	Criteria []struct {
		Text string  `json:"text"`
		Noul float64 `json:"noul"`
	} `json:"criteria"`
	Validation *struct {
		Tally *struct {
			Accept    int `json:"accept"`
			Reject    int `json:"reject"`
			Undecided int `json:"undecided"`
		} `json:"tally"`
	} `json:"validation"`
}

// resolveRequest is the body of POST /graphs/{id}/review/{item}/resolve.
type resolveRequest struct {
	Action string `json:"action"`
	Note   string `json:"note,omitempty"`
}

// resolveResponse is the service's record of the resolution.
type resolveResponse struct {
	Data struct {
		Action     string `json:"action"`
		ItemID     string `json:"item_id"`
		Kind       string `json:"kind"`
		ResolvedBy string `json:"resolved_by"`
	} `json:"data"`
}

// resolveReview is a write. It is not retried: a second resolve of the same
// item answers already_resolved.
func (c *client) resolveReview(ctx context.Context, graphID, itemID string, req resolveRequest) (resolveResponse, error) {
	var out resolveResponse
	err := c.do(ctx, "POST", "/graphs/"+url.PathEscape(graphID)+"/review/"+url.PathEscape(itemID)+"/resolve", req, &out)
	return out, err
}

// reviewItems reads the queue (GET only).
func (c *client) reviewItems(ctx context.Context, graphID string) ([]apiReviewItem, error) {
	raw, err := page[json.RawMessage](ctx, c, "/graphs/"+graphID+"/review")
	if err != nil {
		return nil, err
	}
	items := make([]apiReviewItem, 0, len(raw))
	for _, r := range raw {
		var it apiReviewItem
		if err := json.Unmarshal(r, &it); err != nil {
			return nil, err
		}
		it.Raw = r
		items = append(items, it)
	}
	return items, nil
}
