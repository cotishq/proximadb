package flat

import (
	"math"
	"testing"

	"github.com/cotishq/proximadb/internal/index"
	"github.com/cotishq/proximadb/internal/metric"
)

func TestSearchFindsInsertedVector(t *testing.T) {
	idx, err := New(metric.L2, 32)
	if err != nil {
		t.Fatal(err)
	}

	var query []float32
	for id := uint64(0); id < 100; id++ {
		vec := make([]float32, 32)
		vec[id%32] = 1
		vec[0] += float32(id) * 0.01
		if err := idx.Insert(id, vec); err != nil {
			t.Fatal(err)
		}
		if id == 42 {
			query = append([]float32(nil), vec...)
		}
	}

	hits, err := idx.Search(query, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].ID != 42 {
		t.Fatalf("top hit = %+v, want id 42", hits)
	}
	if math.Abs(float64(hits[0].Distance)) > 1e-5 {
		t.Fatalf("distance = %v, want 0", hits[0].Distance)
	}
}

func TestTieBreaksToSmallerID(t *testing.T) {
	idx, err := New(metric.Cosine, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := idx.Insert(5, []float32{1, 0}); err != nil {
		t.Fatal(err)
	}
	if err := idx.Insert(2, []float32{4, 0}); err != nil {
		t.Fatal(err)
	}

	hits, err := idx.Search([]float32{1, 0}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 || hits[0].ID != 2 || hits[1].ID != 5 {
		t.Fatalf("hits = %+v, want ids 2 then 5", hits)
	}
}

func TestDeleteAndOversizedK(t *testing.T) {
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

	hits, err := idx.Search([]float32{0}, 10)
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

func TestInsertCopiesAndRejectsDuplicate(t *testing.T) {
	idx, err := New(metric.L2, 2)
	if err != nil {
		t.Fatal(err)
	}
	vec := []float32{1, 0}
	if err := idx.Insert(1, vec); err != nil {
		t.Fatal(err)
	}
	vec[0] = 99

	hits, err := idx.Search([]float32{1, 0}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || math.Abs(float64(hits[0].Distance)) > 1e-5 {
		t.Fatalf("stored vector changed with the caller: %+v", hits)
	}
	if err := idx.Insert(1, []float32{0, 1}); err == nil {
		t.Fatal("duplicate id must fail")
	}
}

// Keep the interface import honest if a signature drifts.
var _ index.Index = (*Index)(nil)