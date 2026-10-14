package cmd

import "os"

// stdembRecordSize is the size of the record a go command that carries its
// standard library ends with. The record opens with stdembMagic and must be
// the last bytes of the file (internal/cosmo/embedded in gosmopolitan).
const stdembRecordSize = 64

const stdembMagic = "GOSTDEMB"

// embedsStdlib reports whether the executable at path ends with the record
// the go command finds its embedded standard library through. A build from
// source has none, and a file with bytes after the record cannot find it.
func embedsStdlib(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.Size() < stdembRecordSize {
		return false
	}
	magic := make([]byte, len(stdembMagic))
	if _, err := f.ReadAt(magic, st.Size()-stdembRecordSize); err != nil {
		return false
	}
	return string(magic) == stdembMagic
}
