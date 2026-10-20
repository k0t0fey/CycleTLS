package cycletls

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestTransportErrorEnvelopes(t *testing.T) {
	syscallError := func(message string) error {
		return &net.OpError{Op: "dial", Net: "tcp", Err: &os.SyscallError{Syscall: "connect", Err: errors.New(message)}}
	}
	cases := []struct {
		name   string
		err    error
		status int
		prefix string
	}{
		{"reset", syscallError("connection reset by peer"), 401, "Request returned a Syscall Error"},
		{"no_route", syscallError("no route to host"), 401, "Request returned a Syscall Error"},
		{"unreachable", syscallError("network unreachable"), 401, "Request returned a Syscall Error"},
		{"dns", &net.OpError{Op: "dial", Err: &net.DNSError{Err: "no such host", Name: "localhost"}}, 421, "Request returned a Syscall Error"},
		{"refused", syscallError("connection refused"), 502, "Request returned a Syscall Error"},
		{"address", &net.OpError{Op: "dial", Err: &net.AddrError{Err: "missing port", Addr: "localhost"}}, 405, "Request returned a Syscall Error"},
		{"proxy_status", errors.New("Proxy responded with non 200 code: 407 Proxy Authentication Required StatusCode:407"), 407, "Request returned a Syscall Error"},
		{"deadline", context.DeadlineExceeded, 408, "Request timeout: deadline exceeded"},
		{"cancel", context.Canceled, 408, "Request timeout: deadline exceeded"},
		{"tls", errors.New("uTlsConn.Handshake() error: remote error: tls: handshake failure"), 495, "Request returned a Syscall Error"},
		{"eof", io.EOF, 502, "Request returned a Syscall Error"},
		{"unknown", errors.New("unknown transport error"), 502, "Request returned a Syscall Error"},
		{"invalid_status", errors.New("StatusCode:65536"), 502, "Request returned a Syscall Error"},
	}
	for _, test := range cases {
		for _, phase := range []string{"", "tcp_connect", "proxy_connect"} {
			t.Run(test.name+"/"+phase, func(t *testing.T) {
				cause := test.err
				if phase != "" {
					cause = wrapTransportPhase(phase, cause)
				}
				err := &url.Error{Op: "Get", URL: "https://synthetic-user:synthetic-password@localhost/", Err: cause}
				got := parseError(fmt.Errorf("outer: %w", err))
				if got.StatusCode != test.status || !strings.HasPrefix(got.ErrorMsg, test.prefix) {
					t.Fatalf("got %+v; want %d %q", got, test.status, test.prefix)
				}
				if strings.Contains(got.ErrorMsg+got.debugger, "synthetic-user") || strings.Contains(got.ErrorMsg+got.debugger, "synthetic-password") {
					t.Fatal("proxy credentials escaped error boundary")
				}
				if test.status == 408 && phase != "" && !strings.Contains(got.ErrorMsg, "cycletls_phase="+phase) {
					t.Fatalf("phase was lost: %q", got.ErrorMsg)
				}
			})
		}
	}
	if got := parseError(nil); got.StatusCode != 502 || got.ErrorMsg == "" {
		t.Fatalf("nil error: %+v", got)
	}
}

// Decode the real helper's binary response envelope (without starting a separate process).
func transportDispatch(t *testing.T, options Options) (int, string) {
	t.Helper()
	output := make(chan []byte, 1)
	request := processRequest(cycleTLSRequest{RequestID: "local-test", Options: options})
	go func() { defer close(output); dispatcherAsync(request, output) }()
	timeout := time.After(4 * time.Second)
	status, body := 0, ""
	for {
		select {
		case response, ok := <-output:
			if !ok {
				return status, body
			}
			position := 2 + int(binary.BigEndian.Uint16(response[:2]))
			length := int(binary.BigEndian.Uint16(response[position : position+2]))
			kind := string(response[position+2 : position+2+length])
			position += 2 + length
			if kind == "error" || kind == "response" {
				status = int(binary.BigEndian.Uint16(response[position : position+2]))
				body = string(response[position+2:])
			}
		case <-timeout:
			t.Fatal("helper did not respond")
			return 0, ""
		}
	}
}

func TestTransportProxyResetEnvelope(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		reader := bufio.NewReader(conn)
		for {
			line, err := reader.ReadString('\n')
			if err != nil || line == "\r\n" {
				break
			}
		}
		_ = conn.(*net.TCPConn).SetLinger(0)
	}()
	status, body := transportDispatch(t, Options{
		URL: "https://localhost:1/", Method: "GET", Proxy: "http://synthetic-user:synthetic-password@" + listener.Addr().String(),
		Ja3: DefaultChrome_JA3, UserAgent: "local-test", Timeout: 2, EnableConnectionReuse: false,
	})
	if status == 0 || !strings.Contains(body, "Request returned a Syscall Error") {
		t.Fatalf("status %d body %q", status, body)
	}
	if strings.Contains(body, "synthetic-user") || strings.Contains(body, "synthetic-password") {
		t.Fatal("proxy credentials leaked")
	}
	requireClosed(t, closed)
}

func TestTransportNoReuseClosesAfterEachResponse(t *testing.T) {
	var open atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor != 2 {
			t.Errorf("expected HTTP/2, got %s", r.Proto)
		}
		_, _ = w.Write([]byte("local-ok"))
	}))
	server.EnableHTTP2 = true
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		switch state {
		case http.StateNew:
			open.Add(1)
		case http.StateClosed:
			open.Add(-1)
		}
	}
	server.StartTLS()
	defer server.Close()
	for n := 0; n < 10; n++ {
		status, _ := transportDispatch(t, Options{URL: server.URL, Method: "GET", Ja3: DefaultChrome_JA3,
			UserAgent: "local-test", Timeout: 2, InsecureSkipVerify: true, EnableConnectionReuse: false})
		if status != 200 {
			t.Fatalf("request %d: status %d", n, status)
		}
		until := time.Now().Add(time.Second)
		for open.Load() != 0 && time.Now().Before(until) {
			time.Sleep(time.Millisecond)
		}
		if open.Load() != 0 {
			t.Fatalf("request %d: %d sockets survived", n, open.Load())
		}
	}
}
