package hnsw

import (
	"fmt"
	"math"
	"math/rand"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/cotishq/proximadb/internal/index"
	"github.com/cotishq/proximadb/internal/metric"
)

const (
	defaultM              = 16
	defaultEfConstruction = 200
	defaultEfSearch       = 64
	maxLevelCap           = 16
	// maxVisitedIDs is the largest id that uses the reused stamp slice.
	// Bigger ids fall back to a small map for that one walk.
	maxVisitedIDs = 32_000_000
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

func pushMin(h *[]candidate, c candidate) {
	*h = append(*h, c)
	siftUp(*h, len(*h)-1, true)
}

func popMin(h *[]candidate) candidate {
	old := *h
	c := old[0]
	n := len(old) - 1
	old[0] = old[n]
	*h = old[:n]
	if n > 0 {
		siftDown(*h, 0, true)
	}
	return c
}

func pushMax(h *[]candidate, c candidate) {
	*h = append(*h, c)
	siftUp(*h, len(*h)-1, false)
}

func popMax(h *[]candidate) candidate {
	old := *h
	c := old[0]
	n := len(old) - 1
	old[0] = old[n]
	*h = old[:n]
	if n > 0 {
		siftDown(*h, 0, false)
	}
	return c
}

func siftUp(h []candidate, i int, min bool) {
	for i > 0 {
		p := (i - 1) / 2
		if !heapLess(h[i], h[p], min) {
			break
		}
		h[i], h[p] = h[p], h[i]
		i = p
	}
}

func siftDown(h []candidate, i int, min bool) {
	n := len(h)
	for {
		l := 2*i + 1
		if l >= n {
			return
		}
		best := l
		if r := l + 1; r < n && heapLess(h[r], h[l], min) {
			best = r
		}
		if !heapLess(h[best], h[i], min) {
			return
		}
		h[i], h[best] = h[best], h[i]
		i = best
	}
}

func heapLess(a, b candidate, min bool) bool {
	if min {
		return closer(a, b)
	}
	return closer(b, a)
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
	// nodes[id] is the graph node when id fits in the slice.
	// overflow holds the rare ids at or above maxVisitedIDs.
	nodes    []*node
	overflow map[uint64]*node
	deleted  map[uint64]struct{}
	entry    uint64
	hasEntry bool
	maxLevel int
	// visited[id] == visitGen means this walk already saw id.
	// The slice is reused. Each searchLayer bumps visitGen.
	visited  []uint32
	visitGen uint32
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
		deleted:        make(map[uint64]struct{}),
	}, nil
}

func (idx *Index) node(id uint64) *node {
	if id < uint64(len(idx.nodes)) {
		return idx.nodes[id]
	}
	return idx.overflow[id]
}

func (idx *Index) setNode(id uint64, n *node) {
	if id < uint64(len(idx.nodes)) {
		idx.nodes[id] = n
		return
	}
	if idx.overflow == nil {
		idx.overflow = make(map[uint64]*node)
	}
	idx.overflow[id] = n
}

func (idx *Index) Insert(id uint64, vec []float32) error {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	if len(vec) != idx.dim {
		return fmt.Errorf("hnsw: dimension %d, want %d", len(vec), idx.dim)
	}
	if idx.node(id) != nil {
		return fmt.Errorf("hnsw: id %d already exists", id)
	}
	idx.grow(id)

	copied := make([]float32, len(vec))
	copy(copied, vec)

	level := idx.randomLevel()
	n := &node{
		vec:   copied,
		level: level,
		links: make([][]uint64, level+1),
	}
	idx.setNode(id, n)

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
			neighbor := idx.node(s.id)
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

	if idx.node(id) == nil {
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

// grow makes room for id in the node slice and the stamp slice.
// Capacity doubles so sequential inserts do not reallocate every time.
func (idx *Index) grow(id uint64) {
	if id >= maxVisitedIDs || id < uint64(len(idx.nodes)) {
		return
	}
	need := int(id) + 1
	size := 64
	if n := len(idx.nodes); n > 0 {
		size = n * 2
	}
	if size < need {
		size = need
	}
	if size > maxVisitedIDs {
		size = maxVisitedIDs
	}
	nodes := make([]*node, size)
	copy(nodes, idx.nodes)
	idx.nodes = nodes
	visited := make([]uint32, size)
	copy(visited, idx.visited)
	idx.visited = visited
}

// nextVisit starts a new walk. Zero means the counter wrapped, so the slice is cleared.
func (idx *Index) nextVisit() uint32 {
	g := atomic.AddUint32(&idx.visitGen, 1)
	if g != 0 {
		return g
	}
	for i := range idx.visited {
		atomic.StoreUint32(&idx.visited[i], 0)
	}
	return atomic.AddUint32(&idx.visitGen, 1)
}

// seen reports whether id was already marked on this walk, and marks it if not.
// extra holds ids that do not fit in the stamp slice.
func (idx *Index) seen(id uint64, gen uint32, extra map[uint64]struct{}) (bool, map[uint64]struct{}) {
	if id < uint64(len(idx.visited)) {
		if atomic.LoadUint32(&idx.visited[id]) == gen {
			return true, extra
		}
		atomic.StoreUint32(&idx.visited[id], gen)
		return false, extra
	}
	if extra == nil {
		extra = make(map[uint64]struct{})
	}
	if _, ok := extra[id]; ok {
		return true, extra
	}
	extra[id] = struct{}{}
	return false, extra
}

// searchLayer explores layer starting from ep and keeps the ef closest nodes.
func (idx *Index) searchLayer(q []float32, ep uint64, ef int, layer int) ([]candidate, error) {
	epDist, err := idx.distance(q, idx.node(ep).vec)
	if err != nil {
		return nil, err
	}
	start := candidate{id: ep, dist: epDist}
	gen := idx.nextVisit()
	var extra map[uint64]struct{}
	_, extra = idx.seen(ep, gen, extra)

	// candidates is the closest-first frontier. w keeps the ef closest, furthest on top.
	candidates := []candidate{start}
	w := []candidate{start}

	for len(candidates) > 0 {
		current := popMin(&candidates)
		furthest := w[0]
		if current.dist > furthest.dist {
			break
		}

		for _, nb := range idx.node(current.id).links[layer] {
			var already bool
			already, extra = idx.seen(nb, gen, extra)
			if already {
				continue
			}

			d, err := idx.distance(q, idx.node(nb).vec)
			if err != nil {
				return nil, err
			}
			next := candidate{id: nb, dist: d}
			furthest = w[0]
			if next.dist < furthest.dist || len(w) < ef {
				pushMin(&candidates, next)
				pushMax(&w, next)
				if len(w) > ef {
					popMax(&w)
				}
			}
		}
	}

	// Insert uses nearest[0] as the closest hop. Search takes ids in that order.
	out := []candidate(w)
	sort.Slice(out, func(i, j int) bool { return closer(out[i], out[j]) })
	return out, nil
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
		ev := idx.node(e.id).vec
		diverse := true
		for _, s := range selected {
			between, err := idx.distance(ev, idx.node(s.id).vec)
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
	n := idx.node(id)
	limit := idx.maxLinks(layer)
	if len(n.links[layer]) <= limit {
		return nil
	}

	cands := make([]candidate, 0, len(n.links[layer]))
	for _, nb := range n.links[layer] {
		if nb == id {
			continue
		}
		d, err := idx.distance(n.vec, idx.node(nb).vec)
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

// Compile-time check that *Index implements index.Index.
var _ index.Index = (*Index)(nil)
