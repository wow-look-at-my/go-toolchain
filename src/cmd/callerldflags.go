package cmd

import "strings"

// callerLDFlags is the -ldflags value the caller set in goflags, the GOFLAGS
// environment variable's contents.
//
// The go command applies GOFLAGS to its flag set BEFORE parsing the command
// line, so a -ldflags on the command line replaces the GOFLAGS spelling rather
// than adding to it. This pipeline always passes -ldflags, so without folding
// the caller's value in here a GOFLAGS stamp is discarded with nothing said.
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

// splitGOFLAGS splits s the way the go command splits GOFLAGS: on whitespace,
// with '' or "" around a whole field and no unescaping inside. A quote that
// opens anywhere but at a field's start is ordinary text, which is why
// -ldflags="-X a=b" does NOT survive as a field and the quoted spelling has to
// wrap the flag as well. An unterminated quote yields nothing, matching the go
// command's refusal of the whole value.
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
