package main

import (
	"context"
	"net/http"
	"strconv"
	"time"
)

// streamFrom holds the SSE connection open from the cursor last ("" is live), reconnecting with Last-Event-ID so a
// blip loses nothing (contract §4). status reports live / reconnecting.
func (c *client) streamFrom(ctx context.Context, graphID, last string, fn func(sseEvent), status func(string)) {
	backoff := time.Second
	refreshed := false
	for ctx.Err() == nil {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/graphs/"+graphID+"/stream", nil)
		if err := c.authorize(ctx, req, refreshed); err != nil {
			status("refused: " + err.Error())
			return
		}
		req.Header.Set("Accept", "text/event-stream")
		if last != "" {
			req.Header.Set("Last-Event-ID", last)
		}
		resp, err := (&http.Client{}).Do(req) // no timeout: the body never ends
		if err == nil && resp.StatusCode == http.StatusOK {
			backoff = time.Second
			refreshed = false
			_ = parseSSE(resp.Body, func(ev sseEvent) {
				if ev.ID != "" {
					last = ev.ID
				}
				fn(ev)
			})
			resp.Body.Close()
		} else if resp != nil {
			resp.Body.Close()
			// A 401 gets one reconnect with fresh headers (an expired OAuth
			// token); a second 401 in a row, or a 404, is final.
			if resp.StatusCode == http.StatusUnauthorized && !refreshed {
				refreshed = true
				continue
			}
			if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusUnauthorized {
				status("refused " + strconv.Itoa(resp.StatusCode))
				return
			}
		}
		if ctx.Err() != nil {
			return
		}
		status("reconnecting")
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 15*time.Second)
	}
}
