package vet

import (
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/tools/go/analysis/checker"
	"github.com/wow-look-at-my/go-containers/set"
)

// parseRecorder records every file a load actually parsed, module-relative and
// slash separated, so Verify can prove no tagged file went unseen.
//
// It is locked because x/tools calls packages.Config.ParseFile from an
// errgroup: one goroutine per file. Writing the map and the counter bare killed
// the run with "fatal error: concurrent map writes", and the output watchdog's
// pipes swallowed the panic, so CI showed a bare `exit status 2` with nothing
// above it.
type parseRecorder struct {
	mu    sync.Mutex
	files set.Set[string]
	root  string
	n     int
}

func (r *parseRecorder) record(filename string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.n++
	if rel, err := filepath.Rel(r.root, filename); err == nil && !strings.HasPrefix(rel, "..") {
		r.files.Add(filepath.ToSlash(rel))
	}
}

func (r *parseRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.n
}

// richestVariants maps each package path to the id of the loaded variant with
// the most files, among the root actions of the named analyzer.
//
// packages.Config.Tests loads a package up to four ways: plain, the same code
// recompiled with its internal _test.go files, the external _test package, and
// the generated test main. They share a path and differ in what they can see,
// so an analyzer whose question is about the whole package must be answered by
// the variant that holds all of it.
func richestVariants(graph *checker.Graph, analyzer string) map[string]string {
	best := map[string]string{}
	size := map[string]int{}
	for action := range graph.All() {
		if !action.IsRoot || action.Analyzer.Name != analyzer {
			continue
		}
		p := action.Package
		if n := len(p.Syntax); best[p.PkgPath] == "" || n > size[p.PkgPath] {
			best[p.PkgPath], size[p.PkgPath] = p.ID, n
		}
	}
	return best
}
