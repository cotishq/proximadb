package main

import (
	"flag"
	"fmt"
	"math/rand"
	"os"
	"sort"
	"time"

	"github.com/cotishq/proximadb/internal/index/flat"
	"github.com/cotishq/proximadb/internal/index/hnsw"
	"github.com/cotishq/proximadb/internal/metric"
)

func main() {
	n := flag.Int("n", 1_000_000, "vectors to insert")
	dim := flag.Int("dim", 16, "vector dimension")
	queries := flag.Int("queries", 100, "search queries")
	k := flag.Int("k", 10, "neighbors to retrieve")
	seed := flag.Int64("seed", 42, "rng seed for vectors and queries")
	flag.Parse()
	if err := run(*n, *dim, *queries, *k, *seed); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(n, dim, queries, k int, seed int64) error {
	if n <= 0 || dim <= 0 || queries <= 0 || k <= 0 {
		return fmt.Errorf("n, dim, queries, and k must be positive")
	}

	exact, err := flat.New(metric.L2, dim)
	if err != nil {
		return err
	}
	approx, err := hnsw.New(metric.L2, dim)
	if err != nil {
		return err
	}

	rng := rand.New(rand.NewSource(seed))
	vec := make([]float32, dim)
	insertStart := time.Now()
	for id := 0; id < n; id++ {
		fill(rng, vec)
		if err := exact.Insert(uint64(id), vec); err != nil {
			return err
		}
		if err := approx.Insert(uint64(id), vec); err != nil {
			return err
		}
		if id > 0 && id%100_000 == 0 {
			fmt.Fprintf(os.Stderr, "inserted %d / %d in %s\n", id, n, time.Since(insertStart).Round(time.Second))
		}
	}
	insertElapsed := time.Since(insertStart)

	querySet := make([][]float32, queries)
	for q := 0; q < queries; q++ {
		querySet[q] = make([]float32, dim)
		fill(rng, querySet[q])
	}

	latencies := make([]time.Duration, queries)
	var matched, total int
	for q, query := range querySet {
		want, err := exact.Search(query, k)
		if err != nil {
			return err
		}
		start := time.Now()
		got, err := approx.Search(query, k)
		latencies[q] = time.Since(start)
		if err != nil {
			return err
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

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	recall := float64(matched) / float64(total)
	fmt.Printf("n %d\n", n)
	fmt.Printf("dim %d\n", dim)
	fmt.Printf("metric L2\n")
	fmt.Printf("M 16\n")
	fmt.Printf("efConstruction 200\n")
	fmt.Printf("efSearch 64\n")
	fmt.Printf("queries %d\n", queries)
	fmt.Printf("k %d\n", k)
	fmt.Printf("seed %d\n", seed)
	fmt.Printf("insert %s\n", insertElapsed.Round(time.Millisecond))
	fmt.Printf("recall@%d %.4f\n", k, recall)
	fmt.Printf("p50 %s\n", percentile(latencies, 0.50))
	fmt.Printf("p99 %s\n", percentile(latencies, 0.99))
	return nil
}

func fill(rng *rand.Rand, vec []float32) {
	for i := range vec {
		vec[i] = rng.Float32()*2 - 1
	}
}

// percentile uses the nearest-rank method on an ascending slice.
func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 1 {
		return sorted[0]
	}
	rank := int(p*float64(len(sorted)-1) + 0.5)
	if rank < 0 {
		rank = 0
	}
	if rank >= len(sorted) {
		rank = len(sorted) - 1
	}
	return sorted[rank]
}
