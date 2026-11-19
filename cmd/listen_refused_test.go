package cmd

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Reevit-Platform/cli/internal/api"
	"github.com/Reevit-Platform/cli/internal/config"
)

// streamAnswer is what the fake API does with one stream attempt.
type streamAnswer struct {
	status int
	body   string
}

var (
	deadKey   = streamAnswer{http.StatusUnauthorized, `{"error":"invalid_api_key","code":"invalid_api_key","message":"API key is invalid or revoked"}`}
	noScope   = streamAnswer{http.StatusForbidden, `{"error":"forbidden","code":"forbidden","message":"api key not authorized for org"}`}
	tooMany   = streamAnswer{http.StatusTooManyRequests, `{"code":"too_many_connections","message":"too many concurrent connections"}`}
	apiDown   = streamAnswer{http.StatusBadGateway, `<html>bad gateway</html>`}
	cleanDrop = streamAnswer{http.StatusOK, ""}
)

// listenRun is one `reevit listen` against a fake API that answers stream
// attempts from a script, repeating the last answer once the script runs out.
type listenRun struct {
	attempts atomic.Int32
	stderr   lockedBuffer
	done     chan error
	cancel   context.CancelFunc
}

func startListen(t *testing.T, script ...streamAnswer) *listenRun {
	t.Helper()

	previous := reconnectDelay
	reconnectDelay = 5 * time.Millisecond

	t.Cleanup(func() { reconnectDelay = previous })

	run := &listenRun{done: make(chan error, 1)}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v1/events/stream") {
			w.WriteHeader(http.StatusNotFound)

			return
		}

		n := int(run.attempts.Add(1))
		if n > len(script) {
			n = len(script)
		}

		answer := script[n-1]

		if answer.status == http.StatusOK {
			w.Header().Set("Content-Type", "text/event-stream")
		}

		w.WriteHeader(answer.status)
		_, _ = w.Write([]byte(answer.body))
	}))
	t.Cleanup(server.Close)

	resetFlags()
	t.Cleanup(resetFlags)

	t.Setenv("REEVIT_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	t.Setenv("REEVIT_API_KEY", "pfk_test_refused.sec")
	t.Setenv("REEVIT_API_URL", server.URL)
	t.Setenv("REEVIT_MODE", "")

	ctx, cancel := context.WithCancel(context.Background())
	run.cancel = cancel

	t.Cleanup(cancel)

	var stdout lockedBuffer

	go func() {
		run.done <- ExecuteWith(ctx,
			[]string{"listen", "--forward-to", "http://127.0.0.1:1", "--signing-secret", "whsec_x"},
			strings.NewReader(""), &stdout, &run.stderr)
	}()

	return run
}

// stopped waits for listen to return by itself.
func (r *listenRun) stopped(t *testing.T) error {
	t.Helper()

	select {
	case err := <-r.done:
		return err
	case <-time.After(10 * time.Second):
		t.Fatalf("listen is still running after %d stream attempts; stderr:\n%s",
			r.attempts.Load(), r.stderr.String())

		return nil
	}
}

// keepsGoing waits until listen has made at least n stream attempts, failing
// if it returns first.
func (r *listenRun) keepsGoing(t *testing.T, n int32) {
	t.Helper()

	deadline := time.After(10 * time.Second)

	for r.attempts.Load() < n {
		select {
		case err := <-r.done:
			t.Fatalf("listen stopped after %d stream attempts, want at least %d: %v\n%s",
				r.attempts.Load(), n, err, r.stderr.String())
		case <-deadline:
			t.Fatalf("only %d stream attempts, want %d; stderr:\n%s",
				r.attempts.Load(), n, r.stderr.String())
		case <-time.After(time.Millisecond):
		}
	}
}

// A revoked key used to be retried every 30s for as long as the terminal stayed
// open, under a line that promised a reconnect. It now ends the run with the
// API's own reason and the next step.
func TestListenStopsWhenTheKeyKeepsBeingRefused(t *testing.T) {
	cases := map[string]struct {
		answer streamAnswer
		reason string
		hint   string
	}{
		"revoked key":   {deadKey, "Reevit API 401 (invalid_api_key): API key is invalid or revoked", "reevit login"},
		"missing scope": {noScope, "Reevit API 403 (forbidden): api key not authorized for org", "lacks a required scope"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			run := startListen(t, tc.answer)

			err := run.stopped(t)

			var apiErr *api.APIError
			if !errors.As(err, &apiErr) || apiErr.Status != tc.answer.status {
				t.Fatalf("err = %v, want the API's %d", err, tc.answer.status)
			}

			if got := run.attempts.Load(); got != maxStreamRefusals {
				t.Errorf("stream attempts = %d, want %d", got, maxStreamRefusals)
			}

			if code := ExitCode(err); code != 1 {
				t.Errorf("exit code = %d, want 1", code)
			}

			rendered := renderError(err)
			if !strings.Contains(rendered, tc.reason) || !strings.Contains(rendered, tc.hint) {
				t.Errorf("rendered error = %q, want %q and a hint with %q", rendered, tc.reason, tc.hint)
			}

			// One notice per retry, and none after the refusal that ended it.
			if got := len(reconnectDelays(run.stderr.String())); got != maxStreamRefusals-1 {
				t.Errorf("reconnect notices = %d, want %d:\n%s", got, maxStreamRefusals-1, run.stderr.String())
			}
		})
	}
}

// The case the bug report is about: the stream was healthy, the key was
// revoked while it ran, and every reconnect after that is refused.
func TestListenStopsWhenTheKeyIsRevokedMidSession(t *testing.T) {
	run := startListen(t, cleanDrop, cleanDrop, deadKey)

	if err := run.stopped(t); !streamRefused(err) {
		t.Fatalf("err = %v, want the key refusal", err)
	}

	if got := run.attempts.Load(); got != 2+maxStreamRefusals {
		t.Errorf("stream attempts = %d, want %d", got, 2+maxStreamRefusals)
	}
}

// A 401 is not always final, so a refusal that does not repeat must not end
// the session.
func TestListenSurvivesARefusalThatDoesNotRepeat(t *testing.T) {
	run := startListen(t, deadKey, deadKey, cleanDrop, deadKey, deadKey, cleanDrop)

	run.keepsGoing(t, 8)
	run.cancel()

	if err := <-run.done; ExitCode(err) != 130 {
		t.Fatalf("err = %v (exit %d), want a cancelled run", err, ExitCode(err))
	}
}

// Everything that is not a refusal is still worth waiting out: that is what
// the reconnect loop is for.
func TestListenKeepsReconnectingThroughOutages(t *testing.T) {
	cases := map[string]streamAnswer{
		"rate limited": tooMany,
		"bad gateway":  apiDown,
		"clean close":  cleanDrop,
	}

	for name, answer := range cases {
		t.Run(name, func(t *testing.T) {
			run := startListen(t, answer)

			run.keepsGoing(t, maxStreamRefusals+3)
			run.cancel()

			if err := <-run.done; ExitCode(err) != 130 {
				t.Fatalf("err = %v (exit %d), want a cancelled run", err, ExitCode(err))
			}
		})
	}
}

// The stream's refusal used to read "event stream rejected" whatever the API
// said. It now carries the API's code and message, and keeps the old sentence
// only for a body that has neither.
func TestStreamErrorCarriesTheReasonFromTheBody(t *testing.T) {
	cases := map[string]struct {
		answer  streamAnswer
		code    string
		message string
	}{
		"api envelope": {deadKey, "invalid_api_key", "API key is invalid or revoked"},
		"proxy page":   {apiDown, "http_502", "event stream rejected"},
		"empty body":   {streamAnswer{http.StatusUnauthorized, ""}, "http_401", "event stream rejected"},
		// A body that names its own status must not override the real one.
		"lying status": {streamAnswer{http.StatusBadGateway, `{"status":401,"Status":401}`}, "http_502", "event stream rejected"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.answer.status)
				_, _ = w.Write([]byte(tc.answer.body))
			}))
			defer server.Close()

			c := api.New(config.Config{APIKey: "pfk_test_x.sec", BaseURL: server.URL, Mode: "test"})

			err := c.Stream(context.Background(), "/events/stream", func(api.SSEEvent) {})

			var apiErr *api.APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("err = %v, want an APIError", err)
			}

			if apiErr.Status != tc.answer.status || apiErr.Code != tc.code || apiErr.Message != tc.message {
				t.Errorf("got %d %q %q, want %d %q %q",
					apiErr.Status, apiErr.Code, apiErr.Message, tc.answer.status, tc.code, tc.message)
			}
		})
	}
}
