package cache

import (
	"encoding/json"
	"io"
	"net"
	"os"
	"sync"
	"time"

	"github.com/wow-look-at-my/go-toolchain/src/logger"
)

// noCloseBackend wraps an IBackend and suppresses Close calls.
// Used by the daemon to prevent per-connection Server instances from
// closing the shared remote backend.
type noCloseBackend struct {
	IBackend
}

func (n *noCloseBackend) Close() error { return nil }

// Daemon listens on a Unix socket and serves GOCACHEPROG protocol to
// multiple clients, sharing a single web index and local cache.
type Daemon struct {
	local     LocalStore
	remote    IBackend // real backend (closeable)
	wrapped   IBackend // no-close wrapper for per-connection servers
	listener  net.Listener
	path      string
	wg        sync.WaitGroup
	batch     BatchStats   // shared batch stats, reported to parent
	latency   LatencyStats // web-op latencies of the shared WebBackend (wired once)
	statsMu   sync.Mutex
	statsConn net.Conn // persistent connection to parent's stats socket
}

// NewDaemon creates a cache daemon listening on sockPath.
// It accepts GOCACHEPROG protocol connections over the Unix socket.
// Each connection gets its own Server that shares the underlying backends.
// Batch callbacks are wired once here (not per-connection) with a dedicated
// stats connection that outlives any individual client connection.
func NewDaemon(sockPath string, local LocalStore, remote IBackend) (*Daemon, error) {
	os.Remove(sockPath)
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		return nil, err
	}
	d := &Daemon{
		local:    local,
		remote:   remote,
		listener: ln,
		path:     sockPath,
	}
	if remote != nil {
		d.wrapped = &noCloseBackend{remote}
	}
	// Connect to the stats socket once for the daemon's lifetime.
	if sock := os.Getenv("GOCACHE_STATS_SOCK"); sock != "" {
		if conn, err := net.Dial("unix", sock); err == nil {
			// Wait for the listener's accept-ack (see NewServer).
			conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			var ack [1]byte
			if _, err := conn.Read(ack[:]); err == nil {
				conn.SetReadDeadline(time.Time{})
				d.statsConn = conn
			} else {
				conn.Close()
			}
		}
	}
	// Wire batch callbacks AND web-op latency tracking on the shared
	// WebBackend exactly once, for the daemon's lifetime. Per-connection
	// Servers must never touch these: each NewServer re-pointing wb.Latency
	// at its own tracker was an unsynchronized shared write racing every
	// other connection's in-flight web operations.
	if wb, ok := remote.(*WebBackend); ok {
		wb.Latency = &d.latency
		wireBatchCallbacks(wb, local, d)
	}
	go d.accept()
	return d, nil
}

func (d *Daemon) recordBatchPop(n uint32) {
	d.batch.Populated.Add(n)
	d.sendStat(StatEvent{BatchPop: n})
}

func (d *Daemon) sendStat(ev StatEvent) {
	if d.statsConn == nil {
		return
	}
	data, err := json.Marshal(ev)
	if err != nil {
		return
	}
	d.statsMu.Lock()
	d.statsConn.Write(append(data, '\n'))
	d.statsMu.Unlock()
}

func (d *Daemon) accept() {
	for {
		conn, err := d.listener.Accept()
		if err != nil {
			return
		}
		d.wg.Add(1)
		go d.handleConn(conn)
	}
}

func (d *Daemon) handleConn(conn net.Conn) {
	defer d.wg.Done()
	defer conn.Close()
	// Each connection gets its own Server with shared backends.
	// The no-close wrapper prevents this Server from closing the shared
	// web backend when the connection ends.
	srv := NewServer(d.local, d.wrapped)
	srv.Run(conn, conn)
}

// Close stops the daemon, waits for connections to drain, and cleans up.
func (d *Daemon) Close() {
	d.listener.Close()
	d.wg.Wait()
	if d.remote != nil {
		// Print web backend hit/put/miss breakdown for diagnostics. The
		// hits and puts numbers make it obvious whether the cache is
		// actually working or whether everything is missing.
		if wb, ok := d.remote.(*WebBackend); ok {
			ws := wb.SummarySnapshot()
			// MissTotal excludes the skipped-* counters: they are breakdowns
			// of not-in-index (each such Get already incremented
			// MissNotInIndex before the skip) — adding them would double-count.
			missTotal := ws.MissTotal()
			if ws.Hits > 0 || ws.Puts > 0 || missTotal > 0 {
				logger.Output("cacheprog: web summary: hits=%d puts=%d misses=%d (not-in-index=%d http-404=%d http-err=%d no-outputid=%d read-body=%d decompress=%d checksum=%d buildid=%d modindex=%d network=%d skipped-empty-index=%d skipped-not-in-index=%d skipped-batch-backoff=%d reclaimed-404=%d) put-skipped: known=%d modindex=%d buildid=%d",
					ws.Hits, ws.Puts, missTotal, ws.MissNotInIndex, ws.MissHTTP404, ws.MissHTTPError, ws.MissNoOutputID, ws.MissReadBody, ws.MissDecompress, ws.MissChecksum, ws.MissBuildID, ws.MissModuleIndex, ws.MissNetwork, ws.SkippedEmptyIndex, ws.SkippedNotInIndex, ws.SkippedBatchBackoff, ws.Reclaimed404, ws.PutSkippedKnown, ws.PutRefusedModIndex, ws.PutRefusedBuildID)
			}
		}
		d.remote.Close()
		// Report the shared web-op latencies and the cumulative HTTP-pool
		// usage exactly once, after the backend has fully drained. Doing this
		// per connection (the old flushLatency behavior) merged the shared
		// cumulative pool snapshot additively per connection at the listener,
		// overcounting samples and sums N-fold for N connections.
		if wb, ok := d.remote.(*WebBackend); ok {
			snap := d.latency.Snapshot()
			snap.Pool = wb.Pool.Snapshot()
			d.sendStat(StatEvent{Latency: &snap})
		}
	}
	// Unmount/close the local store after all connections have drained (so no
	// in-flight compiler read can hit a closed FUSE mount or pack handle).
	if d.local != nil {
		if err := d.local.Close(); err != nil {
			logger.Warn("cacheprog: local cache close: %v", err)
		}
	}
	// Close the stats connection AFTER remote.Close() so that batch flush
	// stat events are sent before the connection is torn down.
	if d.statsConn != nil {
		if uc, ok := d.statsConn.(*net.UnixConn); ok {
			uc.CloseWrite()
		} else {
			d.statsConn.Close()
		}
	}
	os.Remove(d.path)
}

// ProxyToDaemon connects to a daemon Unix socket and pipes the
// GOCACHEPROG protocol between stdin/stdout and the daemon.
// This is the fast path: no web index load, no local cache init.
func ProxyToDaemon(sock string) error {
	conn, err := net.Dial("unix", sock)
	if err != nil {
		return err
	}
	defer conn.Close()

	done := make(chan struct{}, 2)

	// stdin → daemon
	go func() {
		io.Copy(conn, os.Stdin)
		// Signal write-done so the daemon's scanner sees EOF.
		if uc, ok := conn.(*net.UnixConn); ok {
			uc.CloseWrite()
		}
		done <- struct{}{}
	}()

	// daemon → stdout
	go func() {
		io.Copy(os.Stdout, conn)
		done <- struct{}{}
	}()

	// Wait for the stdout→ side to finish (daemon closed or sent all data).
	<-done
	return nil
}
