package index

// Hit is one neighbor from a search.
// Distance follows metric: smaller is closer.
type Hit struct {
	ID       uint64
	Distance float32
}

// Index is the storage-agnostic nearest-neighbor API.
// The flat scan and HNSW both implement it, so recall tests can call either one.
type Index interface {
	Insert(id uint64, vec []float32) error
	Search(query []float32, k int) ([]Hit, error)
	Delete(id uint64) error
}