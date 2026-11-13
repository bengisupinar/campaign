package httpserver_test

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/bengisupinar/campaign/internal/platform/httpserver"
)

// receive waits for a value on ch, failing the test if none arrives in time.
func receive[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for result")
		panic("unreachable")
	}
}

func listen(t *testing.T) net.Listener {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	return ln
}

func TestServeFinishesInFlightRequestOnShutdown(t *testing.T) {
	ln := listen(t)
	inFlight := make(chan struct{})
	release := make(chan struct{})
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(inFlight)
		<-release
		_, _ = io.WriteString(w, "done")
	})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	served := make(chan error, 1)
	go func() { served <- httpserver.Serve(ctx, slog.New(slog.DiscardHandler), ln, handler) }()

	type result struct {
		body string
		err  error
	}
	responses := make(chan result, 1)
	go func() {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://"+ln.Addr().String(), nil)
		if err != nil {
			responses <- result{err: err}
			return
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			responses <- result{err: err}
			return
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		responses <- result{body: string(body), err: err}
	}()

	receive(t, inFlight)
	cancel() // the shutdown signal arrives while the request is still being handled

	select {
	case err := <-served:
		t.Fatalf("Serve returned (%v) before the in-flight request finished", err)
	case <-time.After(100 * time.Millisecond):
	}

	close(release)
	if got := receive(t, responses); got.err != nil || got.body != "done" {
		t.Fatalf("in-flight request = (%q, %v), want (\"done\", nil)", got.body, got.err)
	}
	if err := receive(t, served); err != nil {
		t.Fatalf("Serve() = %v, want nil after graceful shutdown", err)
	}
}

func TestServeReturnsListenerErrors(t *testing.T) {
	ln := listen(t)
	_ = ln.Close()

	if err := httpserver.Serve(t.Context(), slog.New(slog.DiscardHandler), ln, http.NotFoundHandler()); err == nil {
		t.Fatal("Serve() = nil on a closed listener, want an error")
	}
}
