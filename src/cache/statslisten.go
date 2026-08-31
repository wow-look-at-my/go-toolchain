package cache

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// maxTrackedActions bounds the per-action outcome map; past it, new IDs count in actionsOverflow instead of growing it unbounded.
const maxTrackedActions = 200_000

// ActionOutcome aggregates the per-action cache events observed for a
// truncated action ID (see truncateActionID, the same
// form cmd/go prints as ActionID in -debug-actiongraph dumps — the join key
// for the build profile).
type ActionOutcome struct {
	// Get is the earliest GET outcome this run; it wins so a later warm re-fetch never hides the original miss.
	Get string
	// Put reports whether a NEW local put was recorded this run; a dedup re-put of an already-cached entry doesn't count.
	Put   bool
	Bytes int64 // largest object size observed, bytes
	GetUS int64 // duration of the earliest GET, microseconds
	PutUS int64 // duration of the local put, microseconds
}

// truncateActionID renders a wire ActionID in cmd/go's truncated form: the
// base64.RawURLEncoding of its leading bytes. Returns "" if too short.
func truncateActionID(id []byte) string {
	if len(id) < buildIDHashSize {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(id[:buildIDHashSize])
}

// withAction attaches per-action outcome fields to a counter StatEvent, so
// the existing per-protocol-op event stream also carries the build
// profile's join data without extra socket writes.
func withAction(ev StatEvent, actionID []byte, op, outcome string, bytes int64, dur time.Duration) StatEvent {
	if id := truncateActionID(actionID); id != "" {
		ev.Action = id
		ev.Op = op
		ev.Outcome = outcome
		ev.Bytes = bytes
		ev.DurUS = dur.Microseconds()
	}
	return ev
}

// StatsListener listens on a unix domain socket and aggregates streaming
// stat events from all cacheprog subprocesses in real-time.
type StatsListener struct {
	listener  net.Listener
	path      string
	local     CacheStats
	remote    CacheStats
	batch     BatchStats
	misses    AtomicCounter
	latency   LatencyStats
	pool      ConcurrencyTracker
	hasRemote atomic.Bool // true if a remote backend was configured (set by caller)
	hasBatch  atomic.Bool
	wg        sync.WaitGroup // tracks active handleConn goroutines
	acceptWg  sync.WaitGroup // tracks the accept loop goroutine

	actionsMu       sync.Mutex
	actions         map[string]*ActionOutcome
	actionsOverflow uint64 // action IDs dropped after maxTrackedActions was hit

	webMu  sync.Mutex
	web    WebSummary
	gotWeb bool // set as soon as a standalone cacheprog reports its web tier
}

// SetHasRemote marks a remote backend as configured, so Stats() includes
// the Remote field even before any remote event arrives.
func (sl *StatsListener) SetHasRemote() {
	sl.hasRemote.Store(true)
}

// SetHasBatch marks batch prefetch as active, so Stats() includes the Batch
// field even before any prefetch event arrives.
func (sl *StatsListener) SetHasBatch() {
	sl.hasBatch.Store(true)
}

// NewStatsListener creates a unix socket and starts accepting connections.
func NewStatsListener(path string) (*StatsListener, error) {
	os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	sl := &StatsListener{listener: ln, path: path}
	sl.acceptWg.Add(1)
	go func() {
		defer sl.acceptWg.Done()
		sl.accept()
	}()
	return sl, nil
}

func (sl *StatsListener) accept() {
	for {
		conn, err := sl.listener.Accept()
		if err != nil {
			return
		}
		sl.wg.Add(1)
		// Ack the connection so the dialer knows it's registered in sl.wg before Close()'s wg.Wait runs.
		conn.Write([]byte{0})
		go sl.handleConn(conn)
	}
}

func (sl *StatsListener) handleConn(conn net.Conn) {
	defer sl.wg.Done()
	defer conn.Close()
	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		var ev StatEvent
		if json.Unmarshal(scanner.Bytes(), &ev) != nil {
			continue
		}
		if ev.LocalHit > 0 {
			sl.local.Hits.Add(ev.LocalHit)
		}
		if ev.LocalPut > 0 {
			sl.local.Puts.Add(ev.LocalPut)
		}
		if ev.RemoteHit > 0 {
			sl.hasRemote.Store(true)
			sl.remote.Hits.Add(ev.RemoteHit)
		}
		if ev.RemotePut > 0 {
			sl.hasRemote.Store(true)
			sl.remote.Puts.Add(ev.RemotePut)
		}
		if ev.Miss > 0 {
			sl.misses.Add(ev.Miss)
		}
		if ev.BatchPop > 0 {
			sl.hasBatch.Store(true)
			sl.batch.Populated.Add(ev.BatchPop)
		}
		if ev.BatchUse > 0 {
			sl.hasBatch.Store(true)
			sl.batch.Used.Add(ev.BatchUse)
		}
		if ev.Latency != nil {
			sl.latency.Merge(*ev.Latency)
			sl.pool.Merge(ev.Latency.Pool)
		}
		if ev.Web != nil {
			sl.recordWeb(*ev.Web)
		}
		if ev.Action != "" {
			sl.recordAction(&ev)
		}
	}
}

// recordAction folds a per-action event into the bounded outcome map.
// Merge policy: the EARLIEST get outcome wins (see ActionOutcome.Get), put is
// sticky, and the largest observed size is kept — so an action that missed,
// was rebuilt, stored, and later re-read warm still reports the miss+put that
// actually cost this run time.
func (sl *StatsListener) recordAction(ev *StatEvent) {
	sl.actionsMu.Lock()
	defer sl.actionsMu.Unlock()
	if sl.actions == nil {
		sl.actions = make(map[string]*ActionOutcome, 4096)
	}
	ao := sl.actions[ev.Action]
	if ao == nil {
		if len(sl.actions) >= maxTrackedActions {
			sl.actionsOverflow++
			return
		}
		ao = &ActionOutcome{}
		sl.actions[ev.Action] = ao
	}
	switch ev.Op {
	case "get":
		if ao.Get == "" {
			ao.Get = ev.Outcome
			ao.GetUS = ev.DurUS
		}
	case "put":
		ao.Put = true
		ao.PutUS = ev.DurUS
	}
	if ev.Bytes > ao.Bytes {
		ao.Bytes = ev.Bytes
	}
}

// Actions returns a snapshot copy of the aggregated per-action outcomes,
// keyed by the truncated action ID.
func (sl *StatsListener) Actions() map[string]ActionOutcome {
	sl.actionsMu.Lock()
	defer sl.actionsMu.Unlock()
	out := make(map[string]ActionOutcome, len(sl.actions))
	for k, v := range sl.actions {
		out[k] = *v
	}
	return out
}

// recordWeb folds a standalone cacheprog's closing web summary into the run's
// aggregate.
func (sl *StatsListener) recordWeb(ws WebSummary) {
	sl.webMu.Lock()
	defer sl.webMu.Unlock()
	MergeWebSummary(&sl.web, ws, !sl.gotWeb)
	sl.gotWeb = true
}

// WebSummary returns what standalone cacheprogs reported this run, or nil when
// none did -- they all proxied to the daemon, which owns its own counters.
func (sl *StatsListener) WebSummary() *WebSummary {
	sl.webMu.Lock()
	defer sl.webMu.Unlock()
	if !sl.gotWeb {
		return nil
	}
	ws := sl.web
	return &ws
}

// ActionsOverflow returns how many distinct action IDs were dropped after the
// outcome map reached maxTrackedActions.
func (sl *StatsListener) ActionsOverflow() uint64 {
	sl.actionsMu.Lock()
	defer sl.actionsMu.Unlock()
	return sl.actionsOverflow
}

// Close stops the listener, waits for connections to drain, and cleans up.
//
// The accept-ack handshake guarantees delivery: a dialer keeps its stats
// connection only after reading the listener's ack, written after the accept
// loop registers it in sl.wg -- so wg.Wait() below covers every connection
// that ever sent an event. The 10ms drain covers a dialer racing Close.
func (sl *StatsListener) Close() {
	// Give the accept loop a short window to drain any connections that are
	// already in the kernel accept queue. listener.Close() would drop them.
	if ul, ok := sl.listener.(*net.UnixListener); ok {
		ul.SetDeadline(time.Now().Add(10 * time.Millisecond))
	} else {
		sl.listener.Close()
	}
	sl.acceptWg.Wait() // wait for accept loop to exit after draining queue
	sl.wg.Wait()       // wait for all handleConn goroutines to finish
	sl.listener.Close()
	os.Remove(sl.path)
}

// Stats returns the aggregated stats.
func (sl *StatsListener) Stats() *ServerStats {
	ss := &ServerStats{
		Local:   &sl.local,
		Misses:  &sl.misses,
		Latency: &sl.latency,
		Pool:    &sl.pool,
	}
	if sl.hasRemote.Load() {
		ss.Remote = &sl.remote
	}
	if sl.hasBatch.Load() {
		ss.Batch = &sl.batch
	}
	return ss
}
