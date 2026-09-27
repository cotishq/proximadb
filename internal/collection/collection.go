package collection

import (
	"fmt"
	"sync"

	"github.com/cotishq/proximadb/internal/index"
	"github.com/cotishq/proximadb/internal/index/hnsw"
	"github.com/cotishq/proximadb/internal/metric"
)

// collection is one named HNSW index.
// Tag filters are added beside this index in a later change.
type collection struct {
	idx *hnsw.Index
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
	s.cols[name] = &collection{idx: idx}
	return nil
}

// Insert stores vec under id in the named collection.
func (s *Store) Insert(name string, id uint64, vec []float32) error {
	col, err := s.get(name)
	if err != nil {
		return err
	}
	return col.idx.Insert(id, vec)
}

// Search returns the k closest vectors in the named collection.
func (s *Store) Search(name string, query []float32, k int) ([]index.Hit, error) {
	col, err := s.get(name)
	if err != nil {
		return nil, err
	}
	return col.idx.Search(query, k)
}

// Delete tombstones id in the named collection.
func (s *Store) Delete(name string, id uint64) error {
	col, err := s.get(name)
	if err != nil {
		return err
	}
	return col.idx.Delete(id)
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

func validMetric(m metric.Metric) bool {
	switch m {
	case metric.Cosine, metric.L2, metric.Dot:
		return true
	default:
		return false
	}
}
