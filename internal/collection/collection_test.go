package collection

import (
	"math"
	"testing"

	"github.com/cotishq/proximadb/internal/index/flat"
	"github.com/cotishq/proximadb/internal/metric"
)

func TestCollectionsAreIsolated(t *testing.T) {
	s := New()
	if err := s.Create("images", metric.L2, 2); err != nil {
		t.Fatal(err)
	}
	if err := s.Create("docs", metric.L2, 2); err != nil {
		t.Fatal(err)
	}

	if err := s.Insert("images", 1, []float32{1, 0}, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Insert("docs", 1, []float32{0, 1}, nil); err != nil {
		t.Fatal(err)
	}

	images, err := s.Search("images", []float32{1, 0}, 1, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 1 || images[0].ID != 1 || images[0].Distance != 0 {
		t.Fatalf("images = %+v, want id 1 at distance 0", images)
	}

	docs, err := s.Search("docs", []float32{1, 0}, 1, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 || docs[0].ID != 1 {
		t.Fatalf("docs = %+v, want id 1", docs)
	}
	if math.Abs(float64(docs[0].Distance)-math.Sqrt2) > 1e-5 {
		t.Fatalf("docs distance = %v, want sqrt(2)", docs[0].Distance)
	}
}

func TestCreateRejectsDuplicatesAndBadNames(t *testing.T) {
	s := New()
	if err := s.Create("", metric.L2, 2); err == nil {
		t.Fatal("empty name must fail")
	}
	if err := s.Create("a", metric.Metric(9), 2); err == nil {
		t.Fatal("unknown metric must fail")
	}
	if err := s.Create("a", metric.L2, 0); err == nil {
		t.Fatal("non-positive dimension must fail")
	}
	if err := s.Create("a", metric.Cosine, 2); err != nil {
		t.Fatal(err)
	}
	if err := s.Create("a", metric.L2, 4); err == nil {
		t.Fatal("duplicate name must fail")
	}
}

func TestMissingCollectionAndDelete(t *testing.T) {
	s := New()
	if err := s.Create("a", metric.L2, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.Create("b", metric.L2, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Search("missing", []float32{1}, 1, nil, 0); err == nil {
		t.Fatal("missing collection must fail")
	}

	if err := s.Insert("a", 1, []float32{1}, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Insert("b", 1, []float32{1}, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("a", 1); err != nil {
		t.Fatal(err)
	}

	a, err := s.Search("a", []float32{1}, 1, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != 0 {
		t.Fatalf("deleted collection a returned %+v", a)
	}
	b, err := s.Search("b", []float32{1}, 1, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) != 1 || b[0].ID != 1 {
		t.Fatalf("collection b = %+v, want id 1", b)
	}
}

func TestTagFilter(t *testing.T) {
	s := New()
	if err := s.Create("items", metric.L2, 1); err != nil {
		t.Fatal(err)
	}
	// id 2 is the nearest untagged neighbor. A filter that only keeps red
	// must skip it and still fill k from the farther red id.
	if err := s.Insert("items", 1, []float32{0}, map[string]string{"color": "red"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Insert("items", 2, []float32{1}, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Insert("items", 3, []float32{10}, map[string]string{"color": "red"}); err != nil {
		t.Fatal(err)
	}

	filtered, err := s.Search("items", []float32{0}, 2, map[string]string{"color": "red"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 2 || filtered[0].ID != 1 || filtered[1].ID != 3 {
		t.Fatalf("filtered = %+v, want ids 1 then 3", filtered)
	}

	open, err := s.Search("items", []float32{0}, 2, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 2 || open[0].ID != 1 || open[1].ID != 2 {
		t.Fatalf("unfiltered = %+v, want ids 1 then 2", open)
	}
}

func TestRareTagFilterFillsK(t *testing.T) {
	s := New()
	oracle, err := flat.New(metric.L2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Create("items", metric.L2, 2); err != nil {
		t.Fatal(err)
	}
	query := []float32{0, 0}
	red := []float32{1, 0}
	filter := map[string]string{"color": "red"}
	for id := uint64(0); id < 190; id++ {
		if err := s.Insert("items", id, query, nil); err != nil {
			t.Fatal(err)
		}
	}
	for id := uint64(190); id < 200; id++ {
		if err := s.Insert("items", id, red, filter); err != nil {
			t.Fatal(err)
		}
		if err := oracle.Insert(id, red); err != nil {
			t.Fatal(err)
		}
	}

	unfiltered, err := s.Search("items", query, 10, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, hit := range unfiltered {
		if hit.ID >= 190 {
			t.Fatalf("unfiltered hit %d, want only untagged neighbors at the query", hit.ID)
		}
	}

	filtered, err := s.Search("items", query, 10, filter, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 10 {
		t.Fatalf("len = %d, want 10", len(filtered))
	}
	want, err := oracle.Search(query, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(want) != 10 {
		t.Fatalf("flat len = %d, want 10", len(want))
	}
	for i := range filtered {
		if filtered[i].ID != want[i].ID {
			t.Fatalf("filtered[%d] = %d, flat = %d", i, filtered[i].ID, want[i].ID)
		}
	}
}

func TestLiveCountIgnoresDeleted(t *testing.T) {
	s := New()
	if err := s.Create("images", metric.L2, 2); err != nil {
		t.Fatal(err)
	}
	if err := s.Insert("images", 1, []float32{1, 0}, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Insert("images", 2, []float32{0, 1}, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("images", 1); err != nil {
		t.Fatal(err)
	}
	if got := s.LiveCount(); got != 1 {
		t.Fatalf("LiveCount = %d, want 1", got)
	}
}

func TestSearchRejectsNegativeEf(t *testing.T) {
	s := New()
	if err := s.Create("images", metric.L2, 2); err != nil {
		t.Fatal(err)
	}
	if err := s.Insert("images", 1, []float32{1, 0}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Search("images", []float32{1, 0}, 1, nil, -1); err == nil {
		t.Fatal("negative efSearch must fail")
	}
	hits, err := s.Search("images", []float32{1, 0}, 1, nil, 16)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].ID != 1 {
		t.Fatalf("hits = %+v, want id 1", hits)
	}
}
