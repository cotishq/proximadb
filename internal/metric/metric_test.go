package metric

import (
	"math"
	"testing"
)

func near(got, want float32) bool {
	return math.Abs(float64(got-want)) < 1e-5
}

func TestL2(t *testing.T) {
	d, err := Distance(L2, []float32{0, 0}, []float32{3, 4})
	if err != nil {
		t.Fatal(err)
	}
	if !near(d, 5) {
		t.Fatalf("L2 = %v, want 5", d)
	}
}

func TestCosine(t *testing.T) {
	same, err := Distance(Cosine, []float32{1, 0}, []float32{2, 0})
	if err != nil {
		t.Fatal(err)
	}
	if !near(same, 0) {
		t.Fatalf("same direction = %v, want 0", same)
	}

	ortho, err := Distance(Cosine, []float32{1, 0}, []float32{0, 1})
	if err != nil {
		t.Fatal(err)
	}
	if !near(ortho, 1) {
		t.Fatalf("orthogonal = %v, want 1", ortho)
	}
}

func TestDotOrdersSameDirectionFirst(t *testing.T) {
	same, err := Distance(Dot, []float32{1, 0}, []float32{1, 0})
	if err != nil {
		t.Fatal(err)
	}
	ortho, err := Distance(Dot, []float32{1, 0}, []float32{0, 1})
	if err != nil {
		t.Fatal(err)
	}
	if !(same < ortho) {
		t.Fatalf("same %v should rank ahead of orthogonal %v", same, ortho)
	}
	if !near(same, -1) || !near(ortho, 0) {
		t.Fatalf("got same %v ortho %v, want -1 and 0", same, ortho)
	}
}

func TestRejectsBadInput(t *testing.T) {
	if _, err := Distance(L2, nil, []float32{1}); err == nil {
		t.Fatal("empty vector must fail")
	}
	if _, err := Distance(L2, []float32{1}, []float32{1, 2}); err == nil {
		t.Fatal("length mismatch must fail")
	}
	if _, err := Distance(Cosine, []float32{0, 0}, []float32{1, 0}); err == nil {
		t.Fatal("zero vector cosine must fail")
	}
	if _, err := Distance(Metric(9), []float32{1}, []float32{1}); err == nil {
		t.Fatal("unknown metric must fail")
	}
}