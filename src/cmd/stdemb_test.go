package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stdembRecord is a record of the size the go command reads from the end of a file.
func stdembRecord() []byte {
	rec := make([]byte, stdembRecordSize)
	copy(rec, stdembMagic)
	return rec
}

func TestEmbedsStdlibReadsTheLastRecord(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, body []byte) string {
		path := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(path, body, 0755))
		return path
	}
	assert.True(t, embedsStdlib(write("embedded", append([]byte("binary bytes"), stdembRecord()...))))
	assert.False(t, embedsStdlib(write("source-build", []byte("binary bytes"))))
	assert.False(t, embedsStdlib(write("stamped-after", append(append([]byte("binary bytes"), stdembRecord()...), []byte("stamp")...))), "bytes after the record hide it")
	assert.False(t, embedsStdlib(write("short", []byte("x"))))
	assert.False(t, embedsStdlib(filepath.Join(dir, "missing")))
}
