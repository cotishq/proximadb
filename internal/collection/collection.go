package collection

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/cotishq/proximadb/internal/index"
	"github.com/cotishq/proximadb/internal/index/hnsw"
	"github.com/cotishq/proximadb/internal/metric"
	"github.com/cotishq/proximadb/internal/wal"
)

// collection is one named HNSW index plus the tags stored beside each id.
// The graph does not know about tags. Empty-filter search walks HNSW.
// A non-empty equality filter starts from the inverted index, then ranks.
type collection struct {
	mu   sync.Mutex
	dim  int
	m    metric.Metric
	idx  *hnsw.Index
	tags map[uint64]map[string]string
	seen map[uint64]struct{}
	live map[uint64]struct{}
	// inv[key][value] is the ids that carry that equality tag.
	inv map[string]map[string]map[uint64]struct{}
}

// Store is the set of named collections in one process.
// A nil log means the store is memory-only. Open attaches a write-ahead log.
type Store struct {
	mu   sync.RWMutex
	cols map[string]*collection
	log  *wal.Log
	dir  string
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
		dir:  dir,
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
// filter is an equality AND: every pair must be present on the hit.
// A nil or empty filter walks HNSW. A non-empty filter takes candidate
// ids from the tag inverted index, then ranks those vectors exactly.
func (s *Store) Search(name string, query []float32, k int, filter map[string]string) ([]index.Hit, error) {
	col, err := s.get(name)
	if err != nil {
		return nil, err
	}
	if k <= 0 {
		return nil, fmt.Errorf("collection: k must be positive")
	}
	if len(filter) == 0 {
		return col.idx.Search(query, k)
	}
	return s.searchFiltered(col, query, k, filter)
}

func (s *Store) searchFiltered(col *collection, query []float32, k int, filter map[string]string) ([]index.Hit, error) {
	if len(query) != col.dim {
		return nil, fmt.Errorf("collection: dimension %d, want %d", len(query), col.dim)
	}
	col.mu.Lock()
	ids := matchingIDs(col, filter)
	col.mu.Unlock()

	hits := make([]index.Hit, 0, len(ids))
	for id := range ids {
		vec, err := col.idx.Vector(id)
		if err != nil {
			continue
		}
		d, err := metric.Distance(col.m, query, vec)
		if err != nil {
			return nil, err
		}
		hits = append(hits, index.Hit{ID: id, Distance: d})
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Distance == hits[j].Distance {
			return hits[i].ID < hits[j].ID
		}
		return hits[i].Distance < hits[j].Distance
	})
	if k > len(hits) {
		k = len(hits)
	}
	return hits[:k], nil
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
		m:    m,
		idx:  idx,
		tags: make(map[uint64]map[string]string),
		seen: make(map[uint64]struct{}),
		live: make(map[uint64]struct{}),
		inv:  make(map[string]map[string]map[uint64]struct{}),
	}
	return nil
}

// Checkpoint rewrites the log as the live store: one create per collection
// and one insert per remaining id. Deletes disappear. Open still replays wal.log.
func (s *Store) Checkpoint() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.log == nil {
		return fmt.Errorf("collection: memory-only store has no log")
	}
	recs, err := s.snapshotRecords()
	if err != nil {
		return err
	}
	path := filepath.Join(s.dir, "wal.log")
	if err := s.log.Close(); err != nil {
		return err
	}
	s.log = nil
	lg, err := wal.Replace(path, recs)
	if err != nil {
		return err
	}
	s.log = lg
	return nil
}

func (s *Store) snapshotRecords() ([][]byte, error) {
	recs := make([][]byte, 0)
	for name, col := range s.cols {
		recs = append(recs, encodeCreate(name, col.m, col.dim))
		for id := range col.live {
			vec, err := col.idx.Vector(id)
			if err != nil {
				return nil, err
			}
			recs = append(recs, encodeInsert(name, id, vec, col.tags[id]))
		}
	}
	return recs, nil
}

func (s *Store) insertInto(col *collection, id uint64, vec []float32, tags map[string]string) error {
	if err := col.idx.Insert(id, vec); err != nil {
		return err
	}
	col.mu.Lock()
	col.tags[id] = tags
	addToInv(col, id, tags)
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
	removeFromInv(col, id, col.tags[id])
	delete(col.tags, id)
	col.mu.Unlock()
	delete(col.live, id)
	return nil
}

func addToInv(col *collection, id uint64, tags map[string]string) {
	for key, value := range tags {
		byValue, ok := col.inv[key]
		if !ok {
			byValue = make(map[string]map[uint64]struct{})
			col.inv[key] = byValue
		}
		ids, ok := byValue[value]
		if !ok {
			ids = make(map[uint64]struct{})
			byValue[value] = ids
		}
		ids[id] = struct{}{}
	}
}

func removeFromInv(col *collection, id uint64, tags map[string]string) {
	for key, value := range tags {
		byValue := col.inv[key]
		if byValue == nil {
			continue
		}
		ids := byValue[value]
		delete(ids, id)
		if len(ids) == 0 {
			delete(byValue, value)
		}
		if len(byValue) == 0 {
			delete(col.inv, key)
		}
	}
}

// matchingIDs returns ids that contain every pair in filter.
func matchingIDs(col *collection, filter map[string]string) map[uint64]struct{} {
	var out map[uint64]struct{}
	first := true
	for key, value := range filter {
		ids := col.inv[key][value]
		if first {
			out = make(map[uint64]struct{}, len(ids))
			for id := range ids {
				out[id] = struct{}{}
			}
			first = false
			continue
		}
		for id := range out {
			if _, ok := ids[id]; !ok {
				delete(out, id)
			}
		}
	}
	if out == nil {
		return map[uint64]struct{}{}
	}
	return out
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

func validMetric(m metric.Metric) bool {
	switch m {
	case metric.Cosine, metric.L2, metric.Dot:
		return true
	default:
		return false
	}
}

// LiveCount returns how many vectors are still searchable.
func (s *Store) LiveCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := 0
	for _, col := range s.cols {
		n += len(col.live)
	}
	return n
}
