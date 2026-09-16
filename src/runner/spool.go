package runner

import (
	"io"
	"sync"
)

// spool receives one output stream of a child as it is produced and serves it
// to a reader at the reader's pace. The child writes into an OS pipe whose
// buffer is a few kilobytes on NT, so a stream nobody is reading blocks the
// child at its next write; the spool's fill goroutine reads that pipe as soon
// as the child starts, and a caller that reads stdout to its end before it
// looks at stderr gets both, in full, from a child that has already exited.
type spool struct {
	mu   sync.Mutex
	cond *sync.Cond
	buf  []byte
	pos  int
	eof  bool
	err  error
}

func newSpool() *spool {
	s := &spool{}
	s.cond = sync.NewCond(&s.mu)
	return s
}

// fill copies r into the spool until r ends, then marks the end.
func (s *spool) fill(r io.Reader) {
	chunk := make([]byte, 32*1024)
	for {
		n, err := r.Read(chunk)
		s.mu.Lock()
		if n > 0 {
			s.buf = append(s.buf, chunk[:n]...)
		}
		if err != nil {
			s.eof = true
			if err != io.EOF {
				s.err = err
			}
		}
		s.cond.Broadcast()
		s.mu.Unlock()
		if err != nil {
			return
		}
	}
}

// Read hands out what the child has produced so far, waiting for more when
// the reader has caught up, and reports the end once the child's stream ends.
func (s *spool) Read(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for s.pos == len(s.buf) && !s.eof {
		s.cond.Wait()
	}
	if s.pos == len(s.buf) {
		if s.err != nil {
			return 0, s.err
		}
		return 0, io.EOF
	}
	n := copy(p, s.buf[s.pos:])
	s.pos += n
	return n, nil
}

// drained blocks until the child's stream has ended.
func (s *spool) drained() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for !s.eof {
		s.cond.Wait()
	}
}
