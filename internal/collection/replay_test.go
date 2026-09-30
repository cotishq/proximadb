package collection

import (
	"math"
	"testing"

	"github.com/cotishq/proximadb/internal/metric"
)

func TestReplayRestoresSearch(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Create("images", metric.L2, 2); err != nil {
		t.Fatal(err)
	}
	if err := s.Insert("images", 1, []float32{1, 0}, map[string]string{"color": "red"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Insert("images", 2, []float32{0, 1}, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("images", 2); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	hits, err := s.Search("images", []float32{1, 0}, 2, map[string]string{"color": "red"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].ID != 1 || math.Abs(float64(hits[0].Distance)) > 1e-5 {
		t.Fatalf("filtered = %+v, want id 1 at distance 0", hits)
	}

	all, err := s.Search("images", []float32{0, 1}, 2, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].ID != 1 {
		t.Fatalf("after delete = %+v, want only id 1", all)
	}
}
