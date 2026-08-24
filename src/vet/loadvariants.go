package vet

import (
	"path/filepath"
	"strings"
	"sync"

	"github.com/wow-look-at-my/go-containers/set"
	"golang.org/x/tools/go/analysis/checker"
)

// parseRecorder records every parsed file, so Verify can prove none went
// unseen. Locked: ParseFile runs one goroutine per file.
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
// packages.Config.Tests loads a package up to four ways (plain, internal test,
// external test, test main); they share a path but differ in what they see,
// so a whole-package question must be answered by the variant with all of it.
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
