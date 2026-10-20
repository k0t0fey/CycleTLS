package cycletls

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

func stalledEndpoint(t *testing.T) (string, <-chan struct{}) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closed := make(chan struct{})
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			close(closed)
			return
		}
		defer conn.Close()
		defer close(closed)
		buf := make([]byte, 4096)
		for {
			if _, err = conn.Read(buf); err != nil {
				return
			}
		}
	}()
	return listener.Addr().String(), closed
}

func requireClosed(t *testing.T, closed <-chan struct{}) {
	t.Helper()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("connection survived cancellation")
	}
}

func requireBounded(t *testing.T, started time.Time, err error, phase string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected error")
	}
	if time.Since(started) > 500*time.Millisecond {
		t.Fatalf("timeout was not respected: %v", time.Since(started))
	}
	var phased *transportPhaseError
	if !errors.As(err, &phased) || phased.phase != phase {
		t.Fatalf("wrong phase: %v", err)
	}
}

func TestTransportContextCONNECTDeadline(t *testing.T) {
	address, closed := stalledEndpoint(t)
	dialer, err := newConnectDialer("http://"+address, "unit-test")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err = dialer.DialContext(ctx, "tcp", "example.invalid:443")
	requireBounded(t, started, err, "proxy_connect")
	requireClosed(t, closed)
}

func TestTransportContextHTTPSProxyDeadline(t *testing.T) {
	address, closed := stalledEndpoint(t)
	dialer, err := newConnectDialer("https://"+address, "unit-test")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err = dialer.DialContext(ctx, "tcp", "example.invalid:443")
	requireBounded(t, started, err, "proxy_connect")
	requireClosed(t, closed)
}

func TestTransportContextCONNECTCancellation(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan struct{})
	closed := make(chan struct{})
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		defer close(closed)
		reader := bufio.NewReader(conn)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			if line == "\r\n" {
				break
			}
		}
		close(accepted)
		_, _ = reader.ReadByte()
	}()
	dialer, err := newConnectDialer("http://"+listener.Addr().String(), "unit-test")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { <-accepted; cancel() }()
	started := time.Now()
	_, err = dialer.DialContext(ctx, "tcp", "example.invalid:443")
	requireBounded(t, started, err, "proxy_connect")
	requireClosed(t, closed)
}

func TestTransportContextTLSDeadline(t *testing.T) {
	address, closed := stalledEndpoint(t)
	rt := newRoundTripper(Browser{JA3: DefaultChrome_JA3, UserAgent: "unit-test"}).(*roundTripper)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := rt.dialTLS(ctx, "tcp", address)
	requireBounded(t, started, err, "tls_handshake")
	requireClosed(t, closed)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("context cause lost: %v", err)
	}
}

func TestTransportContextHTTP2RedialBounded(t *testing.T) {
	address, closed := stalledEndpoint(t)
	rt := newRoundTripper(Browser{JA3: DefaultChrome_JA3, UserAgent: "unit-test"}).(*roundTripper)
	rt.dialTimeout = 60 * time.Millisecond
	started := time.Now()
	_, err := rt.dialTLSHTTP2("tcp", address, nil)
	requireBounded(t, started, err, "tls_handshake")
	requireClosed(t, closed)
}

func TestTransportContextSOCKSLateConnectionCloses(t *testing.T) {
	release := make(chan struct{})
	client, server := net.Pipe()
	defer server.Close()
	dialer := &SocksDialer{socksDial: func(string, string) (net.Conn, error) { <-release; return client, nil }}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := dialer.DialContext(ctx, "tcp", "example.invalid:443")
	requireBounded(t, started, err, "proxy_connect")
	close(release)
	_ = server.SetReadDeadline(time.Now().Add(time.Second))
	if _, err = server.Read(make([]byte, 1)); err == nil {
		t.Fatal("late connection was retained")
	}
}

func TestTransportContextTimeoutRetainsSafePhase(t *testing.T) {
	for _, phase := range []string{"proxy_connect", "tls_handshake"} {
		err := fmt.Errorf("outer: %w", wrapTransportPhase(phase, context.DeadlineExceeded))
		result := parseError(err)
		if result.StatusCode != 408 || !strings.Contains(result.ErrorMsg, "cycletls_phase="+phase) {
			t.Fatalf("bad envelope: %+v", result)
		}
	}
}
