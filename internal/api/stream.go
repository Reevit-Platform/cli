package api

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"strings"
)

// SSEEvent is one parsed server-sent event.
type SSEEvent struct {
	Type string
	Data string
}

// Stream opens an SSE connection and delivers parsed events to handle until
// the context is canceled or the server closes the stream.
func (c *Client) Stream(ctx context.Context, path string, handle func(SSEEvent)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.cfg.BaseURL+"/v1"+path, nil)
	if err != nil {
		return fmt.Errorf("build stream request: %w", err)
	}

	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("X-Reevit-Key", c.cfg.APIKey)
	req.Header.Set("X-Reevit-Mode", c.cfg.Mode)
	req.Header.Set("X-Reevit-Client", "reevit-cli")

	// The default client has a 30s timeout — streams must not.
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return fmt.Errorf("open stream: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		return &APIError{Status: resp.StatusCode, Code: fmt.Sprintf("http_%d", resp.StatusCode), Message: "event stream rejected"}
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var event SSEEvent

	for scanner.Scan() {
		line := scanner.Text()

		switch {
		case line == "":
			if event.Data != "" {
				handle(event)
			}

			event = SSEEvent{}
		case strings.HasPrefix(line, "event:"):
			event.Type = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			if event.Data != "" {
				event.Data += "\n"
			}

			event.Data += strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		}
	}

	if err := scanner.Err(); err != nil && ctx.Err() == nil {
		return fmt.Errorf("stream read: %w", err)
	}

	return ctx.Err()
}
