package hnsw

import (
	"fmt"
	"math"
	"math/rand"
	"sort"
	"sync"

	"github.com/cotishq/proximadb/internal/index"
	"github.com/cotishq/proximadb/internal/metric"
)

const (
	defaultM              = 16
	defaultEfConstruction = 200
	defaultEfSearch       = 64
	maxLevelCap           = 16
)

// candidate is a node the walk is considering.
// dist is the distance from the current query to that node.
type candidate struct {
	id   uint64
	dist float32
}

func closer(a, b candidate) bool {
	if a.dist == b.dist {
		return a.id < b.id
	}
	return a.dist < b.dist
}

type node struct {
	vec   []float32
	level int
	links [][]uint64
}

// Index is an in-memory HNSW graph.
// Search takes the read lock. Insert and Delete take the write lock.
type Index struct {
	mu             sync.RWMutex
	m              metric.Metric
	dim            int
	M              int
	maxM0          int
	efConstruction int
	efSearch       int
	ml             float64
	rng            *rand.Rand
	nodes          map[uint64]*node
	deleted        map[uint64]struct{}
	entry          uint64
	hasEntry       bool
	maxLevel       int
}

// New builds an empty graph for one metric and one dimension.
// M, efConstruction, and efSearch use the defaults the recall target expects.
func New(m metric.Metric, dim int) (*Index, error) {
	if dim <= 0 {
		return nil, fmt.Errorf("hnsw: dimension must be positive")
	}
	return &Index{
		m:              m,
		dim:            dim,
		M:              defaultM,
		maxM0:          defaultM * 2,
		efConstruction: defaultEfConstruction,
		efSearch:       defaultEfSearch,
		ml:             1 / math.Log(float64(defaultM)),
		rng:            rand.New(rand.NewSource(1)),
		nodes:          make(map[uint64]*node),
		deleted:        make(map[uint64]struct{}),
	}, nil
}

func (idx *Index) Insert(id uint64, vec []float32) error {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	if len(vec) != idx.dim {
		return fmt.Errorf("hnsw: dimension %d, want %d", len(vec), idx.dim)
	}
	if _, exists := idx.nodes[id]; exists {
		return fmt.Errorf("hnsw: id %d already exists", id)
	}

	copied := make([]float32, len(vec))
	copy(copied, vec)

	level := idx.randomLevel()
	n := &node{
		vec:   copied,
		level: level,
		links: make([][]uint64, level+1),
	}
	idx.nodes[id] = n

	if !idx.hasEntry {
		idx.entry = id
		idx.hasEntry = true
		idx.maxLevel = level
		return nil
	}

	ep := idx.entry
	// Upper layers only need a single closest hop. They move the walk
	// into the right region before this node grows any links.
	if level < idx.maxLevel {
		for lc := idx.maxLevel; lc > level; lc-- {
			nearest, err := idx.searchLayer(copied, ep, 1, lc)
			if err != nil {
				return err
			}
			ep = nearest[0].id
		}
	}

	// From this node's own top layer down to the bottom, search widely
	// and keep at most M links (2M on layer 0).
	top := min(level, idx.maxLevel)
	for lc := top; lc >= 0; lc-- {
		nearest, err := idx.searchLayer(copied, ep, idx.efConstruction, lc)
		if err != nil {
			return err
		}
		selected, err := idx.selectNeighbors(nearest, idx.maxLinks(lc))
		if err != nil {
			return err
		}
		n.links[lc] = make([]uint64, len(selected))
		for i, s := range selected {
			n.links[lc][i] = s.id
			neighbor := idx.nodes[s.id]
			neighbor.links[lc] = append(neighbor.links[lc], id)
			if err := idx.prune(s.id, lc); err != nil {
				return err
			}
		}
		ep = nearest[0].id
	}

	if level > idx.maxLevel {
		idx.maxLevel = level
		idx.entry = id
	}
	return nil
}

// Search walks from the top layer down and returns the k closest live ids.
func (idx *Index) Search(query []float32, k int) ([]index.Hit, error) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	if k <= 0 {
		return nil, fmt.Errorf("hnsw: k must be positive")
	}
	if len(query) != idx.dim {
		return nil, fmt.Errorf("hnsw: dimension %d, want %d", len(query), idx.dim)
	}
	if !idx.hasEntry {
		return []index.Hit{}, nil
	}

	ep := idx.entry
	for lc := idx.maxLevel; lc > 0; lc-- {
		nearest, err := idx.searchLayer(query, ep, 1, lc)
		if err != nil {
			return nil, err
		}
		ep = nearest[0].id
	}

	ef := idx.efSearch
	if ef < k {
		ef = k
	}
	found, err := idx.searchLayer(query, ep, ef, 0)
	if err != nil {
		return nil, err
	}

	hits := make([]index.Hit, 0, k)
	for _, c := range found {
		if _, dead := idx.deleted[c.id]; dead {
			continue
		}
		hits = append(hits, index.Hit{ID: c.id, Distance: c.dist})
		if len(hits) == k {
			break
		}
	}
	return hits, nil
}

// Delete tombstones id. The node stays in the graph so walks can still cross it.
func (idx *Index) Delete(id uint64) error {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	if _, ok := idx.nodes[id]; !ok {
		return fmt.Errorf("hnsw: id %d not found", id)
	}
	if _, ok := idx.deleted[id]; ok {
		return fmt.Errorf("hnsw: id %d already deleted", id)
	}
	idx.deleted[id] = struct{}{}
	return nil
}

// randomLevel picks how high this node reaches.
// P(level >= 1) is about 1/M, then thins by that factor each layer above.
func (idx *Index) randomLevel() int {
	u := idx.rng.Float64()
	if u < 1e-12 {
		u = 1e-12
	}
	level := int(-math.Log(u) * idx.ml)
	if level > maxLevelCap {
		return maxLevelCap
	}
	return level
}

func (idx *Index) maxLinks(layer int) int {
	if layer == 0 {
		return idx.maxM0
	}
	return idx.M
}

// searchLayer explores layer starting from ep and keeps the ef closest nodes.
func (idx *Index) searchLayer(q []float32, ep uint64, ef int, layer int) ([]candidate, error) {
	epDist, err := idx.distance(q, idx.nodes[ep].vec)
	if err != nil {
		return nil, err
	}
	start := candidate{id: ep, dist: epDist}

	visited := map[uint64]struct{}{ep: {}}
	// candidates is the frontier, closest first. w is the best list, also closest first.
	candidates := []candidate{start}
	w := []candidate{start}

	for len(candidates) > 0 {
		current := candidates[0]
		candidates = candidates[1:]

		furthest := w[len(w)-1]
		if current.dist > furthest.dist {
			break
		}

		for _, nb := range idx.nodes[current.id].links[layer] {
			if _, seen := visited[nb]; seen {
				continue
			}
			visited[nb] = struct{}{}

			d, err := idx.distance(q, idx.nodes[nb].vec)
			if err != nil {
				return nil, err
			}
			next := candidate{id: nb, dist: d}
			furthest = w[len(w)-1]
			if next.dist < furthest.dist || len(w) < ef {
				candidates = insertClosest(candidates, next)
				w = insertClosest(w, next)
				if len(w) > ef {
					w = w[:ef]
				}
			}
		}
	}
	return w, nil
}

// selectNeighbors keeps at most limit neighbors, preferring ones that sit in different directions.
func (idx *Index) selectNeighbors(cands []candidate, limit int) ([]candidate, error) {
	if len(cands) <= limit {
		out := make([]candidate, len(cands))
		copy(out, cands)
		return out, nil
	}

	sorted := make([]candidate, len(cands))
	copy(sorted, cands)
	sort.Slice(sorted, func(i, j int) bool { return closer(sorted[i], sorted[j]) })

	selected := make([]candidate, 0, limit)
	discarded := make([]candidate, 0)
	for _, e := range sorted {
		if len(selected) == limit {
			break
		}
		ev := idx.nodes[e.id].vec
		diverse := true
		for _, s := range selected {
			between, err := idx.distance(ev, idx.nodes[s.id].vec)
			if err != nil {
				return nil, err
			}
			// e is already covered if some chosen neighbor lies closer to e than the query does.
			if e.dist >= between {
				diverse = false
				break
			}
		}
		if diverse {
			selected = append(selected, e)
		} else {
			discarded = append(discarded, e)
		}
	}
	for _, e := range discarded {
		if len(selected) == limit {
			break
		}
		selected = append(selected, e)
	}
	return selected, nil
}

// prune drops the worst links once a node has more than the layer allows.
func (idx *Index) prune(id uint64, layer int) error {
	n := idx.nodes[id]
	limit := idx.maxLinks(layer)
	if len(n.links[layer]) <= limit {
		return nil
	}

	cands := make([]candidate, 0, len(n.links[layer]))
	for _, nb := range n.links[layer] {
		if nb == id {
			continue
		}
		d, err := idx.distance(n.vec, idx.nodes[nb].vec)
		if err != nil {
			return err
		}
		cands = append(cands, candidate{id: nb, dist: d})
	}
	selected, err := idx.selectNeighbors(cands, limit)
	if err != nil {
		return err
	}
	links := make([]uint64, len(selected))
	for i, s := range selected {
		links[i] = s.id
	}
	n.links[layer] = links
	return nil
}

func (idx *Index) distance(a, b []float32) (float32, error) {
	return metric.Distance(idx.m, a, b)
}

// insertClosest inserts c so the slice stays ordered closest-first.
func insertClosest(list []candidate, c candidate) []candidate {
	i := 0
	for i < len(list) && !closer(c, list[i]) {
		i++
	}
	list = append(list, candidate{})
	copy(list[i+1:], list[i:])
	list[i] = c
	return list
}

// Compile-time check that *Index implements index.Index.
var _ index.Index = (*Index)(nil)
