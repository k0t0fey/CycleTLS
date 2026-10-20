package cycletls

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"regexp"
	"strconv"
	"strings"
)

type errorMessage struct {
	StatusCode int
	debugger   string
	ErrorMsg   string
	Op         string
}

// Remove URL userinfo before errors reach the SDK, logs or debug output.
var errorURLUserinfo = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://)[^/\s]*@`)

func safeErrorText(err error) string {
	if err == nil {
		return "unknown transport error"
	}
	return errorURLUserinfo.ReplaceAllString(err.Error(), "${1}[redacted]@")
}

func lastString(ss []string) string {
	return ss[len(ss)-1]
}

// func createErrorString(err: string) (msg, debugger string) {
func createErrorString(err error) (msg, debugger string) {
	msg = fmt.Sprintf("Request returned a Syscall Error: %s", safeErrorText(err))
	debugger = safeErrorText(err)
	return
}

func createErrorMessage(StatusCode int, err error, op string) errorMessage {
	var msg string

	// For timeout errors, provide a clean, user-friendly message
	if op == "timeout" {
		msg = "Request timeout: deadline exceeded"
		var phased *transportPhaseError
		if errors.As(err, &phased) {
			msg += "; cycletls_phase=" + phased.phase
		}
	} else {
		// For other errors, provide detailed debugging information
		msg = fmt.Sprintf("Request returned a Syscall Error: %s", safeErrorText(err))
	}

	debugger := safeErrorText(err)
	return errorMessage{StatusCode: StatusCode, debugger: debugger, ErrorMsg: msg, Op: op}
}

func parseError(err error) (errormessage errorMessage) {
	if err == nil {
		return createErrorMessage(502, err, "transport")
	}

	// Check for context.DeadlineExceeded (client timeout)
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return createErrorMessage(408, err, "timeout")
	}

	var timeoutErr net.Error
	if errors.As(err, &timeoutErr) && timeoutErr.Timeout() {
		return createErrorMessage(408, err, "timeout")
	}
	var phased *transportPhaseError
	if errors.As(err, &phased) && phased.phase == "tls_handshake" {
		return createErrorMessage(495, err, "tls_handshake")
	}
	httpError := string(err.Error())
	//todo - clean this up

	// Check for TLS certificate errors (should return 495)
	if strings.Contains(httpError, "uTlsConn.Handshake() error") ||
		strings.Contains(httpError, "tls: failed to verify certificate") ||
		strings.Contains(httpError, "x509: certificate") ||
		strings.Contains(httpError, "certificate verify failed") ||
		strings.Contains(httpError, "certificate has expired") ||
		strings.Contains(httpError, "certificate signed by unknown authority") {
		return createErrorMessage(495, err, "certificate")
	}

	// Check for connection refused errors (should return 502)
	if strings.Contains(httpError, "connection refused") ||
		strings.Contains(httpError, "connect: connection refused") ||
		strings.Contains(httpError, "dial tcp") && strings.Contains(httpError, "connect: connection refused") {
		return createErrorMessage(502, err, "connection")
	}

	// Check for common timeout error messages
	if strings.Contains(httpError, "context deadline exceeded") ||
		strings.Contains(httpError, "Client.Timeout") ||
		strings.Contains(httpError, "context cancellation while reading body") ||
		strings.Contains(httpError, "i/o timeout") ||
		strings.Contains(httpError, "timeout") {
		return createErrorMessage(408, err, "timeout")
	}

	status := lastString(strings.Split(httpError, "StatusCode:"))
	StatusCode, _ := strconv.Atoi(status)
	if StatusCode >= 100 && StatusCode <= 599 {
		msg, debugger := createErrorString(err)
		return errorMessage{StatusCode: StatusCode, debugger: debugger, ErrorMsg: msg}
	}
	var opErr *net.OpError
	var syscallErr *os.SyscallError
	var addrErr *net.AddrError
	var dnsErr *net.DNSError
	op := "transport"
	if errors.As(err, &opErr) {
		op = opErr.Op
	}
	switch {
	case errors.As(err, &syscallErr):
		return createErrorMessage(401, err, op)
	case errors.As(err, &addrErr):
		return createErrorMessage(405, err, op)
	case errors.As(err, &dnsErr):
		return createErrorMessage(421, err, op)
	case opErr != nil:
		return createErrorMessage(421, err, op)
	default:
		// EOF and unknown errors must never look like a successful status-zero response.
		return createErrorMessage(502, err, op)
	}
}

type errExtensionNotExist struct {
	Context string
}

func (w *errExtensionNotExist) Error() string {
	return fmt.Sprintf("Extension {{ %s }} is not Supported by CycleTLS please raise an issue", w.Context)
}

func raiseExtensionError(info string) *errExtensionNotExist {
	return &errExtensionNotExist{
		Context: info,
	}
}
