package cache

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
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
	local    LocalStore
	remote   IBackend // real backend (closeable)
	wrapped  IBackend // no-close wrapper for per-connection servers
	listener net.Listener
	path     string
	wg       sync.WaitGroup
	batch    BatchStats // shared batch stats, reported to parent
	statsMu  sync.Mutex
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
			d.statsConn = conn
		}
	}
	// Wire batch callbacks on the shared WebBackend once, using the
	// daemon's long-lived stats connection instead of per-connection ones.
	if wb, ok := remote.(*WebBackend); ok {
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
			hits := wb.Stats.Hits.Load()
			puts := wb.Stats.Puts.Load()
			notInIndex := wb.MissNotInIndex.Load()
			http404 := wb.MissHTTP404.Load()
			httpErr := wb.MissHTTPError.Load()
			noOutputID := wb.MissNoOutputID.Load()
			readBody := wb.MissReadBody.Load()
			decompress := wb.MissDecompress.Load()
			network := wb.MissNetwork.Load()
			missTotal := notInIndex + http404 + httpErr + noOutputID + readBody + decompress + network
			if hits > 0 || puts > 0 || missTotal > 0 {
				fmt.Fprintf(os.Stderr, "cacheprog: web summary: hits=%d puts=%d misses=%d (not-in-index=%d http-404=%d http-err=%d no-outputid=%d read-body=%d decompress=%d network=%d)\n",
					hits, puts, missTotal, notInIndex, http404, httpErr, noOutputID, readBody, decompress, network)
			}
		}
		d.remote.Close()
	}
	// Unmount/close the local store after all connections have drained (so no
	// in-flight compiler read can hit a closed FUSE mount or pack handle).
	if d.local != nil {
		if err := d.local.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "cacheprog: local cache close: %v\n", err)
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
