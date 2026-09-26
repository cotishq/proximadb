package hnsw

import (
	"math/rand"
	"testing"

	"github.com/cotishq/proximadb/internal/index"
	"github.com/cotishq/proximadb/internal/index/flat"
	"github.com/cotishq/proximadb/internal/metric"
)

func TestSearchFindsInsertedVector(t *testing.T) {
	idx, err := New(metric.L2, 4)
	if err != nil {
		t.Fatal(err)
	}
	vec := []float32{1, 0, 0, 0}
	if err := idx.Insert(7, vec); err != nil {
		t.Fatal(err)
	}
	hits, err := idx.Search(vec, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].ID != 7 || hits[0].Distance != 0 {
		t.Fatalf("hits = %+v, want id 7 at distance 0", hits)
	}
}

func TestDuplicateInsert(t *testing.T) {
	idx, err := New(metric.L2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := idx.Insert(1, []float32{1, 0}); err != nil {
		t.Fatal(err)
	}
	if err := idx.Insert(1, []float32{0, 1}); err == nil {
		t.Fatal("duplicate id must fail")
	}
}

func TestDeleteHidesID(t *testing.T) {
	idx, err := New(metric.L2, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []uint64{1, 2, 3} {
		if err := idx.Insert(id, []float32{float32(id)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := idx.Delete(2); err != nil {
		t.Fatal(err)
	}
	hits, err := idx.Search([]float32{2}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 {
		t.Fatalf("len = %d, want 2", len(hits))
	}
	for _, h := range hits {
		if h.ID == 2 {
			t.Fatal("deleted id 2 was returned")
		}
	}
}

func TestRecallAt10(t *testing.T) {
	const (
		n       = 2000
		dim     = 16
		queries = 50
		k       = 10
	)
	exact, err := flat.New(metric.L2, dim)
	if err != nil {
		t.Fatal(err)
	}
	approx, err := New(metric.L2, dim)
	if err != nil {
		t.Fatal(err)
	}

	rng := rand.New(rand.NewSource(42))
	for id := uint64(0); id < n; id++ {
		vec := make([]float32, dim)
		for d := 0; d < dim; d++ {
			vec[d] = rng.Float32()*2 - 1
		}
		if err := exact.Insert(id, vec); err != nil {
			t.Fatal(err)
		}
		if err := approx.Insert(id, vec); err != nil {
			t.Fatal(err)
		}
	}

	var matched, total int
	for q := 0; q < queries; q++ {
		query := make([]float32, dim)
		for d := 0; d < dim; d++ {
			query[d] = rng.Float32()*2 - 1
		}
		want, err := exact.Search(query, k)
		if err != nil {
			t.Fatal(err)
		}
		got, err := approx.Search(query, k)
		if err != nil {
			t.Fatal(err)
		}
		truth := make(map[uint64]struct{}, len(want))
		for _, hit := range want {
			truth[hit.ID] = struct{}{}
		}
		for _, hit := range got {
			if _, ok := truth[hit.ID]; ok {
				matched++
			}
		}
		total += len(want)
	}

	recall := float64(matched) / float64(total)
	t.Logf("recall@10 = %.4f", recall)
	if recall < 0.95 {
		t.Fatalf("recall@10 = %f, want >= 0.95", recall)
	}
}

var _ index.Index = (*Index)(nil)
