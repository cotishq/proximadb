package collection

import (
	"fmt"
	"sync"

	"github.com/cotishq/proximadb/internal/index"
	"github.com/cotishq/proximadb/internal/index/hnsw"
	"github.com/cotishq/proximadb/internal/metric"
)

// collection is one named HNSW index plus the tags stored beside each id.
// The graph does not know about tags. Search filters them after the walk.
type collection struct {
	mu   sync.Mutex
	idx  *hnsw.Index
	tags map[uint64]map[string]string
}

// Store is the set of named collections in one process.
type Store struct {
	mu   sync.RWMutex
	cols map[string]*collection
}

// New returns an empty store.
func New() *Store {
	return &Store{cols: make(map[string]*collection)}
}

// Create registers a collection with a fixed metric and dimension.
func (s *Store) Create(name string, m metric.Metric, dim int) error {
	if name == "" {
		return fmt.Errorf("collection: name is empty")
	}
	if !validMetric(m) {
		return fmt.Errorf("collection: unknown metric %d", m)
	}

	idx, err := hnsw.New(m, dim)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.cols[name]; exists {
		return fmt.Errorf("collection: %q already exists", name)
	}
	s.cols[name] = &collection{
		idx:  idx,
		tags: make(map[uint64]map[string]string),
	}
	return nil
}

// Insert stores vec under id in the named collection.
// tags may be nil. The map is copied, so the caller can reuse it.
func (s *Store) Insert(name string, id uint64, vec []float32, tags map[string]string) error {
	col, err := s.get(name)
	if err != nil {
		return err
	}
	copied, err := copyTags(tags)
	if err != nil {
		return err
	}
	if err := col.idx.Insert(id, vec); err != nil {
		return err
	}
	col.mu.Lock()
	col.tags[id] = copied
	col.mu.Unlock()
	return nil
}

// Search returns the k closest vectors in the named collection.
// filter is an equality check: every pair must be present on the hit.
// A nil or empty filter keeps every hit. Candidates are over-fetched so
// dropping non-matches can still fill k.
func (s *Store) Search(name string, query []float32, k int, filter map[string]string) ([]index.Hit, error) {
	col, err := s.get(name)
	if err != nil {
		return nil, err
	}
	if k <= 0 {
		return nil, fmt.Errorf("collection: k must be positive")
	}
	limit := k * 4
	if limit < k {
		limit = k
	}
	hits, err := col.idx.Search(query, limit)
	if err != nil {
		return nil, err
	}

	col.mu.Lock()
	defer col.mu.Unlock()
	matched := make([]index.Hit, 0, k)
	for _, hit := range hits {
		if !matchTags(col.tags[hit.ID], filter) {
			continue
		}
		matched = append(matched, hit)
		if len(matched) == k {
			break
		}
	}
	return matched, nil
}

// Delete tombstones id in the named collection and drops its tags.
func (s *Store) Delete(name string, id uint64) error {
	col, err := s.get(name)
	if err != nil {
		return err
	}
	if err := col.idx.Delete(id); err != nil {
		return err
	}
	col.mu.Lock()
	delete(col.tags, id)
	col.mu.Unlock()
	return nil
}

func (s *Store) get(name string) (*collection, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	col, ok := s.cols[name]
	if !ok {
		return nil, fmt.Errorf("collection: %q not found", name)
	}
	return col, nil
}

func copyTags(tags map[string]string) (map[string]string, error) {
	if len(tags) == 0 {
		return nil, nil
	}
	copied := make(map[string]string, len(tags))
	for key, value := range tags {
		if key == "" {
			return nil, fmt.Errorf("collection: tag key is empty")
		}
		copied[key] = value
	}
	return copied, nil
}

// matchTags reports whether tags contains every pair in filter.
func matchTags(tags, filter map[string]string) bool {
	for key, value := range filter {
		if tags[key] != value {
			return false
		}
	}
	return true
}

func validMetric(m metric.Metric) bool {
	switch m {
	case metric.Cosine, metric.L2, metric.Dot:
		return true
	default:
		return false
	}
}
