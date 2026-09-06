package cmd

import (
	"strconv"
	"strings"
)

// parseLsofPipeHandles reads `lsof -F pftnd` output into pid -> (handle -> peer).
// A file set opens with `f<fd>` and carries `tPIPE`, `d0xHANDLE` and `n->0xPEER`.
//
// The field ORDER is lsof's own, never the order the -F flags name, and lsof
// emits `d` and `n` ahead of `t`. So the type cannot gate a field as it
// arrives. Every field is collected instead, and the record is judged when the
// next `f` or `p` closes it.
func parseLsofPipeHandles(out string) map[int]map[uint64]uint64 {
	byPID := make(map[int]map[uint64]uint64)
	pid := 0
	var rec lsofFile

	flush := func() {
		handle, peer, ok := rec.pipeEnds()
		if !ok || pid == 0 {
			return
		}
		if byPID[pid] == nil {
			byPID[pid] = make(map[uint64]uint64)
		}
		byPID[pid][handle] = peer
	}

	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		switch line[0] {
		case 'p':
			flush()
			rec = lsofFile{}
			pid, _ = strconv.Atoi(line[1:])
		case 'f':
			flush()
			rec = lsofFile{}
		case 't':
			rec.fileType = line[1:]
		case 'd':
			rec.dev = line[1:]
		case 'n':
			rec.name = line[1:]
		}
	}
	flush()
	return byPID
}

// lsofFile is an `-F` file record, collected field by field.
type lsofFile struct {
	fileType string
	dev      string
	name     string
}

// pipeEnds reports this record's own handle and the handle it points at, when
// the record is a pipe carrying both.
func (r lsofFile) pipeEnds() (handle, peer uint64, ok bool) {
	if r.fileType != "PIPE" {
		return 0, 0, false
	}
	handle, ok = parseHexHandle(r.dev)
	if !ok || handle == 0 {
		return 0, 0, false
	}
	peer, ok = parseHexHandle(strings.TrimPrefix(r.name, "->"))
	if !ok {
		return 0, 0, false
	}
	return handle, peer, true
}

func parseHexHandle(s string) (uint64, bool) {
	s = strings.TrimPrefix(s, "0x")
	if s == "" {
		return 0, false
	}
	v, err := strconv.ParseUint(s, 16, 64)
	return v, err == nil
}

func joinPids(pids []int) string {
	parts := make([]string, len(pids))
	for i, p := range pids {
		parts[i] = strconv.Itoa(p)
	}
	return strings.Join(parts, ",")
}
