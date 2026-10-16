package cmd

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// noBackoff replaces the wait between attempts and records each wait.
func noBackoff(t *testing.T) *[]time.Duration {
	t.Helper()
	var waits []time.Duration
	prev := buildhostSleep
	buildhostSleep = func(d time.Duration) { waits = append(waits, d) }
	t.Cleanup(func() { buildhostSleep = prev })
	return &waits
}

// statusServer answers each request with the next status of the list, and the last one for every request after.
func statusServer(t *testing.T, statuses ...int) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := int(calls.Add(1))
		code := statuses[min(n, len(statuses))-1]
		w.WriteHeader(code)
		if code == http.StatusOK {
			_, _ = w.Write([]byte(`{"value":"ok"}`))
			return
		}
		_, _ = w.Write([]byte("error code: " + http.StatusText(code)))
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func TestBuildhostDoSucceedsAfterABadGateway(t *testing.T) {
	t.Serial()
	waits := noBackoff(t)
	srv, calls := statusServer(t, http.StatusBadGateway, http.StatusOK)
	var answer runLockBody
	require.NoError(t, runLockDo(http.MethodGet, srv.URL, "tok", nil, &answer))
	assert.Equal(t, "ok", answer.Value)
	assert.Equal(t, int32(2), calls.Load())
	assert.Equal(t, []time.Duration{buildhostBackoffBase}, *waits)
}

func TestBuildhostDoGivesUpAfterTheCapAndNamesEveryAttempt(t *testing.T) {
	t.Serial()
	waits := noBackoff(t)
	srv, calls := statusServer(t, http.StatusBadGateway)
	err := runLockDo(http.MethodGet, srv.URL, "tok", nil, &runLockBody{})
	require.Error(t, err)
	assert.Equal(t, int32(buildhostAttempts), calls.Load())
	assert.Len(t, *waits, buildhostAttempts-1)
	assert.Equal(t, 2*buildhostBackoffBase, (*waits)[1])
	for i := 1; i <= buildhostAttempts; i++ {
		assert.Contains(t, err.Error(), "attempt "+string(rune('0'+i))+": 502 Bad Gateway")
	}
}

func TestBuildhostDoFailsAtOnceOnAClientError(t *testing.T) {
	t.Serial()
	waits := noBackoff(t)
	srv, calls := statusServer(t, http.StatusBadRequest, http.StatusOK)
	err := runLockDo(http.MethodGet, srv.URL, "tok", nil, &runLockBody{})
	require.ErrorContains(t, err, "400 Bad Request")
	assert.Equal(t, int32(1), calls.Load())
	assert.Empty(t, *waits)
}

func TestBuildhostDoFailsAtOnceOnAServerError(t *testing.T) {
	t.Serial()
	waits := noBackoff(t)
	srv, calls := statusServer(t, http.StatusInternalServerError, http.StatusOK)
	err := runLockDo(http.MethodGet, srv.URL, "tok", nil, &runLockBody{})
	require.ErrorContains(t, err, "500 Internal Server Error")
	assert.Equal(t, int32(1), calls.Load())
	assert.Empty(t, *waits)
}

func TestBuildhostDoRetriesAResetConnection(t *testing.T) {
	t.Serial()
	waits := noBackoff(t)
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			conn, _, err := w.(http.Hijacker).Hijack()
			require.NoError(t, err)
			conn.Close()
			return
		}
		_, _ = w.Write([]byte(`{"value":"ok"}`))
	}))
	t.Cleanup(srv.Close)
	var answer runLockBody
	require.NoError(t, runLockDo(http.MethodGet, srv.URL, "tok", nil, &answer))
	assert.Equal(t, int32(2), calls.Load())
	assert.Len(t, *waits, 1)
}

func TestReleaseDoRetriesAGatewayTimeout(t *testing.T) {
	t.Serial()
	noBackoff(t)
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusGatewayTimeout)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"version":"7"}`))
	}))
	t.Cleanup(srv.Close)
	var created struct {
		Version string `json:"version"`
	}
	require.NoError(t, releaseDo(srv.URL, "tok", []byte(`{}`), &created))
	assert.Equal(t, "7", created.Version)
	assert.Equal(t, int32(2), calls.Load())
}
