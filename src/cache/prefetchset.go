package cache

import (
	"sync"

	"github.com/wow-look-at-my/go-containers/set"
)

// prefetchSet records the action IDs a batch GET stored, so the GET path can
// report the share of the prefetch anything read back.
type prefetchSet struct {
	mu   sync.Mutex
	keys set.Set[string]
}

func newPrefetchSet() *prefetchSet {
	return &prefetchSet{keys: set.New[string]()}
}

// add records an action ID the batch callback stored.
func (p *prefetchSet) add(actionID string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.keys.Add(actionID)
	p.mu.Unlock()
}

// take reports whether this action ID was prefetched and consumes it, so
// repeated reads of an entry cannot push the used count past the populated
// count.
func (p *prefetchSet) take(actionID string) bool {
	if p == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.keys.Contains(actionID) {
		return false
	}
	p.keys.Remove(actionID)
	return true
}
