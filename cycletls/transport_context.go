package cycletls

import (
	"context"
	"fmt"
	"net"
)

// Static phases survive the SDK error envelope without exposing proxy URLs or credentials.
type transportPhaseError struct {
	phase string
	cause error
}

func (e *transportPhaseError) Error() string {
	return fmt.Sprintf("cycletls_phase=%s: %v", e.phase, e.cause)
}
func (e *transportPhaseError) Unwrap() error { return e.cause }
func wrapTransportPhase(phase string, err error) error {
	if err == nil {
		return nil
	}
	if _, ok := err.(*transportPhaseError); ok {
		return err
	}
	return &transportPhaseError{phase, err}
}

// CONNECT's ReadResponse ignores Request.Context. Closing the raw socket unblocks both write/read.
// The returned stop waits for the watcher, so ownership cannot race with a successful TLS handoff.
func closeConnOnCancellation(ctx context.Context, conn net.Conn) func() {
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-done:
		}
	}()
	return func() { close(done); <-stopped }
}
