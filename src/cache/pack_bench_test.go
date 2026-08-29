package cache

import (
	"bytes"
	"github.com/stretchr/testify/require"
	"testing"
)

// Benchmarks for the warm read paths: GetVerified (the GET RPC gate) and
// GetByOutputVerified (the FUSE Lookup gate). Before the verified-read memo
// (verify.go), both re-read and re-hashed the FULL body on every call — a
// pipeline of several cmd/go invocations re-GETting the same actions paid that
// cost repeatedly for every warm hit.

func benchPackStore(b *testing.B, size int) (s *PackStore, aid, oid string) {
	b.Helper()
	s, err := OpenPackStore(b.TempDir())
	require.Nil(b, err)

	b.Cleanup(func() { _ = s.Close() })
	body := make([]byte, size)
	for i := range body {
		body[i] = byte(i*131 + 17)
	}
	aid, oid = hexID(1), casID(body)
	_, err = s.Put(aid, oid, bytes.NewReader(body))
	require.Nil(b, err)

	return s, aid, oid
}

func BenchmarkPackGetVerifiedWarm64KiB(b *testing.B) {
	s, aid, _ := benchPackStore(b, 64<<10)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, ok := s.GetVerified(aid)
		require.True(b, ok)

	}
}

func BenchmarkPackGetByOutputVerifiedWarm1MiB(b *testing.B) {
	s, _, oid := benchPackStore(b, 1<<20)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, ok := s.GetByOutputVerified(oid)
		require.True(b, ok)

	}
}
