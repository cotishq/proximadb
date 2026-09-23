package metric

import (
	"fmt"
	"math"
)

// Metric selects how two vectors are compared.
// Every metric here is a distance: a smaller value is a closer pair.
type Metric uint8

const (
	Cosine Metric = iota
	L2
	Dot
)

// Distance reports how far a is from b under m.
func Distance(m Metric, a, b []float32) (float32, error) {
	if len(a) == 0 || len(b) == 0 {
		return 0, fmt.Errorf("metric: empty vector")
	}
	if len(a) != len(b) {
		return 0, fmt.Errorf("metric: length %d != %d", len(a), len(b))
	}

	switch m {
	case Cosine:
		return cosine(a, b)
	case L2:
		return l2(a, b), nil
	case Dot:
		return dot(a, b), nil
	default:
		return 0, fmt.Errorf("metric: unknown metric %d", m)
	}
}

func l2(a, b []float32) float32 {
	var sum float32
	for i := range a {
		d := a[i] - b[i]
		sum += d * d
	}
	return float32(math.Sqrt(float64(sum)))
}

func dot(a, b []float32) float32 {
	var sum float32
	for i := range a {
		sum += a[i] * b[i]
	}
	return -sum
}

func cosine(a, b []float32) (float32, error) {
	var dp, na, nb float32
	for i := range a {
		dp += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	if na == 0 || nb == 0 {
		return 0, fmt.Errorf("metric: cosine undefined for zero vector")
	}
	sim := dp / (sqrt32(na) * sqrt32(nb))
	return 1 - sim, nil
}

func sqrt32(v float32) float32 {
	return float32(math.Sqrt(float64(v)))
}