package mapset

// A literal whose values are all true is a set.
var byteEncodingNames = map[string]bool{ // want `map\[…\]bool with every value true is a set`
	"ISO-8859-1": true,
	"LATIN1":     true,
}

func localLiteral() bool {
	hosts := map[string]bool{ // want `map\[…\]bool with every value true is a set`
		"github.com": true,
	}
	return hosts["github.com"]
}

// A map made empty and only ever written true, read, ranged and deleted from
// is a set too.
func madeEmpty(names []string) int {
	seen := make(map[string]bool) // want `map\[…\]bool is only ever used as a set`
	for _, n := range names {
		if seen[n] {
			continue
		}
		seen[n] = true
	}
	delete(seen, "x")
	for n := range seen {
		_ = n
	}
	return len(seen)
}

var pkgLevel = map[string]bool{} // want `map\[…\]bool is only ever used as a set`

func writePkgLevel(k string) bool {
	pkgLevel[k] = true
	return pkgLevel[k]
}

// A map that carries a real value is untouched.
var platformIdents = map[string]bool{
	"cgo":    true,
	"ignore": false,
}

var counts = map[string]int{
	"one": 1,
}

// A struct{} value map already carries nothing; which one to write is the
// author's call.
var alreadyASet = make(map[string]struct{})

func useAlreadyASet(k string) bool {
	alreadyASet[k] = struct{}{}
	_, ok := alreadyASet[k]
	return ok
}

// The comma-ok read tells absent from present-and-false, so this is a map.
func commaOK(k string) bool {
	m := make(map[string]bool)
	m[k] = true
	v, ok := m[k]
	return v && ok
}

// A value that is not the constant true is a real value.
func writesComputed(k string, v bool) int {
	m := make(map[string]bool)
	m[k] = v
	return len(m)
}

// A map handed to another function can be read anywhere.
func escapes(k string) int {
	m := make(map[string]bool)
	m[k] = true
	return consume(m)
}

func consume(m map[string]bool) int { return len(m) }

// Ranging over the values reads them.
func rangesValues(k string) bool {
	m := make(map[string]bool)
	m[k] = true
	for _, v := range m {
		if v {
			return true
		}
	}
	return false
}

// A map that is only read never proves anything about its values.
func onlyRead(m map[string]bool, k string) bool {
	return m[k]
}
