// buildhostAttempts is how many times one request is tried before the build fails.

package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"syscall"
	"time"
)

const (
	// buildhostAttempts is how many times one request is tried before the build fails.
	buildhostAttempts = 5
	// buildhostBackoffBase is the wait after the first failed attempt. Each later wait doubles it.
	buildhostBackoffBase = 4 * time.Second
	// buildhostReplyLimit bounds how much of an answer is read.
	buildhostReplyLimit = 1 << 16
)

// buildhostSleep is the wait between attempts, as a seam so a test does not sleep.
var buildhostSleep = time.Sleep

// buildhostReply is one answer from a server that is not a transient failure.
type buildhostReply struct {
	Status string
	Code   int
	Body   []byte
}

// transientStatus reports whether a gateway status says the request probably never reached the service.
func transientStatus(code int) bool {
	switch code {
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return false
}

// transientError reports whether a transport error is a reset, a premature close or a timeout.
func transientError(err error) bool {
	if errors.Is(err, syscall.ECONNRESET) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var timeout interface{ Timeout() bool }
	return errors.As(err, &timeout) && timeout.Timeout()
}

// Any other status comes back as a reply for the caller to judge. Any other transport error fails at once. A
// create-release request that reached the server before the gateway failed leaves one unused release number.
// The run lock keeps the first claimed number, so the build publishes under one.
func buildhostDo(method, target, token string, body []byte) (buildhostReply, error) {
	var attempts []string
	delay := buildhostBackoffBase
	for attempt := 1; attempt <= buildhostAttempts; attempt++ {
		reply, err := buildhostOnce(method, target, token, body)
		switch {
		case err == nil && !transientStatus(reply.Code):
			return reply, nil
		case err == nil:
			attempts = append(attempts, fmt.Sprintf("attempt %d: %s: %s", attempt, reply.Status, strings.TrimSpace(string(reply.Body))))
		case transientError(err):
			attempts = append(attempts, fmt.Sprintf("attempt %d: %v", attempt, err))
		default:
			return buildhostReply{}, err
		}
		if attempt < buildhostAttempts {
			buildhostSleep(delay)
			delay *= 2
		}
	}
	return buildhostReply{}, fmt.Errorf("giving up after %d attempts, %s", buildhostAttempts, strings.Join(attempts, "; "))
}

// buildhostOnce sends a request one time.
func buildhostOnce(method, target, token string, body []byte) (buildhostReply, error) {
	req, err := http.NewRequest(method, target, bytes.NewReader(body))
	if err != nil {
		return buildhostReply{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := runLockClient.Do(req)
	if err != nil {
		return buildhostReply{}, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, buildhostReplyLimit))
	if err != nil {
		return buildhostReply{}, err
	}
	return buildhostReply{Status: resp.Status, Code: resp.StatusCode, Body: data}, nil
}
