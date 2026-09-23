package flat

import (
	"fmt"
	"sort"

	"github.com/cotishq/proximadb/internal/index"
	"github.com/cotishq/proximadb/internal/metric"
)

// Index is an exact nearest-neighbor scan.
// It is the ground truth that HNSW recall is measured against.
type Index struct {
	m    metric.Metric
	dim  int
	vecs map[uint64][]float32
}

// New builds an empty exact index for one metric and one dimension.
func New(m metric.Metric, dim int) (*Index, error) {
	if dim <= 0 {
		return nil, fmt.Errorf("flat: dimension must be positive")
	}
	return &Index{
		m:    m,
		dim:  dim,
		vecs: make(map[uint64][]float32),
	}, nil
}

// Insert stores a copy of vec under id.
func (idx *Index) Insert(id uint64, vec []float32) error {
	if len(vec) != idx.dim {
		return fmt.Errorf("flat: dimension %d, want %d", len(vec), idx.dim)
	}
	if _, ok := idx.vecs[id]; ok {
		return fmt.Errorf("flat: id %d already exists", id)
	}
	copied := make([]float32, len(vec))
	copy(copied, vec)
	idx.vecs[id] = copied
	return nil
}

// Search returns the k closest vectors. Ties go to the smaller id.
func (idx *Index) Search(query []float32, k int) ([]index.Hit, error) {
	if k <= 0 {
		return nil, fmt.Errorf("flat: k must be positive")
	}
	if len(query) != idx.dim {
		return nil, fmt.Errorf("flat: dimension %d, want %d", len(query), idx.dim)
	}

	hits := make([]index.Hit, 0, len(idx.vecs))
	for id, vec := range idx.vecs {
		d, err := metric.Distance(idx.m, query, vec)
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
	out := make([]index.Hit, k)
	copy(out, hits[:k])
	return out, nil
}

// Delete removes id from the index.
func (idx *Index) Delete(id uint64) error {
	if _, ok := idx.vecs[id]; !ok {
		return fmt.Errorf("flat: id %d not found", id)
	}
	delete(idx.vecs, id)
	return nil
}

// Compile-time check that *Index implements index.Index.
var _ index.Index = (*Index)(nil)
