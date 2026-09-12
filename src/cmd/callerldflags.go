package cmd

import "strings"

// callerLDFlags is the -ldflags the caller set in GOFLAGS. The go command
// applies GOFLAGS before parsing argv, so a -ldflags on the command line
// REPLACES it, and this pipeline always passes its own.
func callerLDFlags(goflags string) string {
	var values []string
	for _, field := range splitGOFLAGS(goflags) {
		name, value, assigned := strings.Cut(field, "=")
		if assigned && (name == "-ldflags" || name == "--ldflags") {
			values = append(values, value)
		}
	}
	return strings.Join(values, " ")
}

// splitGOFLAGS splits s as the go command does: on whitespace, with a quote pair
// around a WHOLE field and no unescaping inside. A quote opening anywhere else is
// ordinary text, so -ldflags="-X a=b" is not a field and the quotes have to wrap
// the flag too. An unterminated quote yields nothing, as the go command does.
func splitGOFLAGS(s string) []string {
	var fields []string
	for {
		s = strings.TrimLeft(s, " \t\n\r")
		if s == "" {
			return fields
		}
		if quote := s[0]; quote == '"' || quote == '\'' {
			end := strings.IndexByte(s[1:], quote)
			if end < 0 {
				return nil
			}
			fields = append(fields, s[1:1+end])
			s = s[end+2:]
			continue
		}
		end := strings.IndexAny(s, " \t\n\r")
		if end < 0 {
			return append(fields, s)
		}
		fields = append(fields, s[:end])
		s = s[end:]
	}
}
