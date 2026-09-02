package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Reevit-Platform/cli/internal/api"
	"github.com/Reevit-Platform/cli/internal/ui"
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

// cobra caches the context on each subcommand and only fills it when it is
// nil, so the second `listen` of a process used to run with the first one's
// context and ignore cancellation entirely. The golden suite runs `listen`
// before this file's cancellation tests, which is exactly that second run.
func TestCancellationSurvivesASecondRunOfTheSameCommand(t *testing.T) {
	resetFlags()
	t.Cleanup(resetFlags)

	var warmup bytes.Buffer

	if err := ExecuteWith(context.Background(), []string{"listen", "--help"},
		strings.NewReader(""), &warmup, &warmup); err != nil {
		t.Fatalf("warm-up run: %v", err)
	}

	resetFlags()

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
	t.Setenv("REEVIT_API_KEY", "pfk_test_second.sec")
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
		if got := ExitCode(err); got != 130 {
			t.Fatalf("ExitCode = %d, want 130 (err %v)", got, err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the second run of listen ignored cancellation")
	}
}

// RenderError is the one place an error becomes the text a user reads, so its
// three silent cases and its hint plumbing are asserted directly rather than
// only through the goldens.
func TestRenderError(t *testing.T) {
	t.Parallel()

	t.Run("a hint from the error is printed under it", func(t *testing.T) {
		t.Parallel()

		err := &api.APIError{Status: 401, Code: "unauthorized", Message: "invalid key"}

		got := RenderError(fmt.Errorf("that key was rejected: %w", err), ui.Styler{})
		if !strings.Contains(got, "error: that key was rejected") {
			t.Fatalf("rendered = %q, want the cause", got)
		}

		if !strings.Contains(got, "reevit login") {
			t.Fatalf("rendered = %q, want the 401 hint", got)
		}
	})

	t.Run("an error with nothing to suggest gets no hint block", func(t *testing.T) {
		t.Parallel()

		got := RenderError(errors.New("nothing selected — nothing to do"), ui.Styler{})
		if got != "error: nothing selected — nothing to do" {
			t.Fatalf("rendered = %q, want the bare error", got)
		}
	})

	t.Run("doctor's own verdict is not printed twice", func(t *testing.T) {
		t.Parallel()

		err := ExitError{Code: exitDoctor, Err: errDoctorFailed}

		if got := RenderError(err, ui.Styler{}); got != "" {
			t.Fatalf("rendered = %q, want nothing: doctor already printed its verdict", got)
		}

		if got := ExitCode(err); got != 3 {
			t.Fatalf("ExitCode = %d, want 3", got)
		}
	})

	t.Run("a cancelled run prints its own message without the error prefix", func(t *testing.T) {
		t.Parallel()

		got := RenderError(ExitError{Code: 130, Err: errors.New("setup cancelled")}, ui.Styler{})
		if got != "setup cancelled" {
			t.Fatalf("rendered = %q, want the bare message", got)
		}
	})

	t.Run("a wrapped context cancellation prints nothing", func(t *testing.T) {
		t.Parallel()

		got := RenderError(fmt.Errorf("stream: %w", context.Canceled), ui.Styler{})
		if got != "" {
			t.Fatalf("rendered = %q, want nothing for Ctrl-C", got)
		}
	})

	t.Run("an unreachable API names the URL to check", func(t *testing.T) {
		t.Parallel()

		err := &url.Error{
			Op:  "Get",
			URL: "http://127.0.0.1:1/v1/payments",
			Err: errors.New("connect: connection refused"),
		}

		got := RenderError(fmt.Errorf("list payments: %w", err), ui.Styler{})
		if !strings.Contains(got, "REEVIT_API_URL") {
			t.Fatalf("rendered = %q, want the network hint", got)
		}
	})
}

// A wrong argument count is a usage error, not a runtime failure: scripts
// distinguish "you called me wrong" (2) from "I tried and failed" (1).
func TestUsageErrorsExitTwo(t *testing.T) {
	t.Parallel()

	err := exactArgs(1)(triggerCmd, []string{})
	if err == nil {
		t.Fatal("exactArgs accepted the wrong argument count")
	}

	// The literal 2 is deliberate: comparing against exitUsage would still
	// pass if someone changed the constant.
	if got := ExitCode(err); got != 2 {
		t.Fatalf("ExitCode = %d, want 2 (err %v)", got, err)
	}
}
