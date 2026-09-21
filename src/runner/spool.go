package runner

import (
	"io"
	"sync"
)

// spool drains a child's pipe as it fills, so reading one stream to its
// end never blocks the child on the other.
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
// the reader has caught up, and reports the end a single time the child's stream ends.
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
