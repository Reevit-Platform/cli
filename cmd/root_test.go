package cmd

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestExitCode(t *testing.T) {
	if got := ExitCode(ExitError{Code: 130, Err: errors.New("cancelled")}); got != 130 {
		t.Fatalf("ExitCode() = %d, want 130", got)
	}
	if got := ExitCode(errors.New("failed")); got != 1 {
		t.Fatalf("ExitCode() = %d, want 1", got)
	}
}

// Cancelling the context must unwind a long-running command promptly and
// report the conventional 130, rather than leaving it blocked on a stream
// that only Go's default signal handling would ever kill.
func TestExecuteWithCancelledContextExits130(t *testing.T) {
	resetFlags()
	t.Cleanup(resetFlags)

	blocked := make(chan struct{})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v1/events/stream") {
			w.WriteHeader(http.StatusNotFound)

			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}

		close(blocked)
		<-r.Context().Done()
	}))
	defer server.Close()

	t.Setenv("REEVIT_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	t.Setenv("REEVIT_API_KEY", "pfk_test_cancel.sec")
	t.Setenv("REEVIT_API_URL", server.URL)
	t.Setenv("REEVIT_MODE", "")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		<-blocked
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()

	var stdout, stderr bytes.Buffer

	done := make(chan error, 1)

	go func() {
		done <- ExecuteWith(ctx,
			[]string{"listen", "--forward-to", "http://127.0.0.1:1", "--signing-secret", "whsec_x"},
			strings.NewReader(""), &stdout, &stderr)
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("listen returned nil, want a cancellation error")
		}

		if got := ExitCode(err); got != 130 {
			t.Fatalf("ExitCode = %d, want 130 (err %v)", got, err)
		}

		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want it to wrap context.Canceled", err)
		}

		if got := renderError(err); got != "" {
			t.Fatalf("renderError = %q, want nothing printed for a cancelled run", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("listen did not return within 2s of cancellation")
	}
}
