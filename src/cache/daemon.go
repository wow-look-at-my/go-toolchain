package cache

import (
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
	local    *LocalCache
	remote   IBackend // real backend (closeable)
	wrapped  IBackend // no-close wrapper for per-connection servers
	listener net.Listener
	path     string
	wg       sync.WaitGroup
}

// NewDaemon creates a cache daemon listening on sockPath.
// It accepts GOCACHEPROG protocol connections over the Unix socket.
// Each connection gets its own Server that shares the underlying backends.
func NewDaemon(sockPath string, local *LocalCache, remote IBackend) (*Daemon, error) {
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
	go d.accept()
	return d, nil
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
		d.remote.Close()
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
