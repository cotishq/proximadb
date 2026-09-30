package collection

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/cotishq/proximadb/internal/index"
	"github.com/cotishq/proximadb/internal/index/hnsw"
	"github.com/cotishq/proximadb/internal/metric"
	"github.com/cotishq/proximadb/internal/wal"
)

// collection is one named HNSW index plus the tags stored beside each id.
// The graph does not know about tags. Search filters them after the walk.
type collection struct {
	mu   sync.Mutex
	dim  int
	idx  *hnsw.Index
	tags map[uint64]map[string]string
	seen map[uint64]struct{}
	live map[uint64]struct{}
}

// Store is the set of named collections in one process.
// A nil log means the store is memory-only. Open attaches a write-ahead log.
type Store struct {
	mu   sync.RWMutex
	cols map[string]*collection
	log  *wal.Log
}

// New returns an empty in-memory store. Data disappears when the process exits.
func New() *Store {
	return &Store{cols: make(map[string]*collection)}
}

// Open creates or reopens a store whose mutations are appended under dir.
// Existing records are replayed into empty HNSW indexes before Open returns.
func Open(dir string) (*Store, error) {
	if dir == "" {
		return nil, fmt.Errorf("collection: data directory is empty")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	lg, err := wal.Open(filepath.Join(dir, "wal.log"))
	if err != nil {
		return nil, err
	}
	s := &Store{
		cols: make(map[string]*collection),
		log:  lg,
	}
	for _, payload := range lg.Records() {
		if err := s.apply(payload); err != nil {
			lg.Close()
			return nil, err
		}
	}
	return s, nil
}

// Close closes the log. It is safe to call on an in-memory store.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.log == nil {
		return nil
	}
	err := s.log.Close()
	s.log = nil
	return err
}

// Create registers a collection with a fixed metric and dimension.
func (s *Store) Create(name string, m metric.Metric, dim int) error {
	if name == "" {
		return fmt.Errorf("collection: name is empty")
	}
	if !validMetric(m) {
		return fmt.Errorf("collection: unknown metric %d", m)
	}
	if dim <= 0 {
		return fmt.Errorf("collection: dimension must be positive")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.cols[name]; exists {
		return fmt.Errorf("collection: %q already exists", name)
	}
	if s.log != nil {
		if err := s.log.Append(encodeCreate(name, m, dim)); err != nil {
			return err
		}
	}
	return s.addCollection(name, m, dim)
}

// Insert stores vec under id in the named collection.
// tags may be nil. The map is copied, so the caller can reuse it.
func (s *Store) Insert(name string, id uint64, vec []float32, tags map[string]string) error {
	copied, err := copyTags(tags)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	col, ok := s.cols[name]
	if !ok {
		return fmt.Errorf("collection: %q not found", name)
	}
	if len(vec) != col.dim {
		return fmt.Errorf("collection: dimension %d, want %d", len(vec), col.dim)
	}
	if _, exists := col.seen[id]; exists {
		return fmt.Errorf("collection: id %d already exists", id)
	}
	if s.log != nil {
		if err := s.log.Append(encodeInsert(name, id, vec, copied)); err != nil {
			return err
		}
	}
	return s.insertInto(col, id, vec, copied)
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
	s.mu.Lock()
	defer s.mu.Unlock()
	col, ok := s.cols[name]
	if !ok {
		return fmt.Errorf("collection: %q not found", name)
	}
	if _, ok := col.live[id]; !ok {
		return fmt.Errorf("collection: id %d not found", id)
	}
	if s.log != nil {
		if err := s.log.Append(encodeDelete(name, id)); err != nil {
			return err
		}
	}
	return s.deleteFrom(col, id)
}

func (s *Store) apply(payload []byte) error {
	if len(payload) == 0 {
		return fmt.Errorf("collection: empty log record")
	}
	switch payload[0] {
	case opCreate:
		name, m, dim, err := decodeCreate(payload)
		if err != nil {
			return err
		}
		return s.addCollection(name, m, dim)
	case opInsert:
		name, id, vec, tags, err := decodeInsert(payload)
		if err != nil {
			return err
		}
		col, ok := s.cols[name]
		if !ok {
			return fmt.Errorf("collection: %q not found", name)
		}
		return s.insertInto(col, id, vec, tags)
	case opDelete:
		name, id, err := decodeDelete(payload)
		if err != nil {
			return err
		}
		col, ok := s.cols[name]
		if !ok {
			return fmt.Errorf("collection: %q not found", name)
		}
		return s.deleteFrom(col, id)
	default:
		return fmt.Errorf("collection: unknown log op %d", payload[0])
	}
}

func (s *Store) addCollection(name string, m metric.Metric, dim int) error {
	if _, exists := s.cols[name]; exists {
		return fmt.Errorf("collection: %q already exists", name)
	}
	idx, err := hnsw.New(m, dim)
	if err != nil {
		return err
	}
	s.cols[name] = &collection{
		dim:  dim,
		idx:  idx,
		tags: make(map[uint64]map[string]string),
		seen: make(map[uint64]struct{}),
		live: make(map[uint64]struct{}),
	}
	return nil
}

func (s *Store) insertInto(col *collection, id uint64, vec []float32, tags map[string]string) error {
	if err := col.idx.Insert(id, vec); err != nil {
		return err
	}
	col.mu.Lock()
	col.tags[id] = tags
	col.mu.Unlock()
	col.seen[id] = struct{}{}
	col.live[id] = struct{}{}
	return nil
}

func (s *Store) deleteFrom(col *collection, id uint64) error {
	if err := col.idx.Delete(id); err != nil {
		return err
	}
	col.mu.Lock()
	delete(col.tags, id)
	col.mu.Unlock()
	delete(col.live, id)
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
