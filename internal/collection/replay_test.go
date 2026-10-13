package collection

import (
	"math"
	"path/filepath"
	"testing"

	"github.com/cotishq/proximadb/internal/metric"
	"github.com/cotishq/proximadb/internal/wal"
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

func TestCheckpointDropsDeleted(t *testing.T) {
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
	if err := s.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	lg, err := wal.Open(filepath.Join(dir, "wal.log"))
	if err != nil {
		t.Fatal(err)
	}
	if n := len(lg.Records()); n != 2 {
		t.Fatalf("records = %d, want 2 (create + live insert)", n)
	}
	if err := lg.Close(); err != nil {
		t.Fatal(err)
	}

	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.LiveCount() != 1 {
		t.Fatalf("LiveCount = %d, want 1", s.LiveCount())
	}
	hits, err := s.Search("images", []float32{1, 0}, 2, map[string]string{"color": "red"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].ID != 1 {
		t.Fatalf("hits = %+v, want id 1", hits)
	}
	if err := s.Insert("images", 3, []float32{0, 1}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestCheckpointMemoryOnly(t *testing.T) {
	s := New()
	if err := s.Checkpoint(); err == nil {
		t.Fatal("memory-only checkpoint must fail")
	}
}
