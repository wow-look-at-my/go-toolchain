package mapset

// Every map[K]struct{} is a set, whatever position it sits in.
var seen = make(map[string]struct{}) // want `map\[…\]struct\{\} is a set`

type registry struct {
	names map[string]struct{} // want `map\[…\]struct\{\} is a set`
}

func consume(m map[int]struct{}) int { // want `map\[…\]struct\{\} is a set`
	return len(m)
}

// A literal whose values are all true is a set too.
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

// A marker on the line suppresses the report.
var allowedInline = map[string]struct{}{} // go-toolchain:allow-mapset json shape is fixed

// go-toolchain:allow-mapset the false entry is load-bearing below
var allowedAbove = map[string]bool{
	"unix":   true,
	"ignore": false,
}

// A map that carries a real value is untouched.
var platformIdents = map[string]bool{
	"cgo":    true,
	"ignore": false,
}

var counts = map[string]int{
	"one": 1,
}

// An empty literal says nothing about its values.
var empty = map[string]bool{}

// Values that are not the constant true say nothing either.
func computed(ok bool) map[string]bool {
	return map[string]bool{"a": ok}
}
