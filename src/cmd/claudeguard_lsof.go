package cmd

import (
	"strconv"
	"strings"
)

// parseLsofPipeHandles reads `lsof -F pftnd` output into pid -> (handle -> peer).
// A PIPE line set is `p<pid>` then `f<fd>` `tPIPE` `d0xHANDLE` `n->0xPEER`.
func parseLsofPipeHandles(out string) map[int]map[uint64]uint64 {
	byPID := make(map[int]map[uint64]uint64)
	pid := 0
	isPipe := false
	var handle uint64
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		switch line[0] {
		case 'p':
			pid, _ = strconv.Atoi(line[1:])
			isPipe = false
			handle = 0
		case 'f':
			isPipe = false
			handle = 0
		case 't':
			isPipe = line[1:] == "PIPE"
		case 'd':
			if isPipe {
				handle, _ = parseHexHandle(line[1:])
			}
		case 'n':
			if !isPipe || handle == 0 || pid == 0 {
				continue
			}
			peer, ok := parseHexHandle(strings.TrimPrefix(line[1:], "->"))
			if !ok {
				continue
			}
			if byPID[pid] == nil {
				byPID[pid] = make(map[uint64]uint64)
			}
			byPID[pid][handle] = peer
		}
	}
	return byPID
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
