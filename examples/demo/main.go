// Command demo loads a tiny product catalog into a running ProximaDB and
// searches it. The sentences stay in this program. The database stores the
// vectors and the color tags.
//
// The vectors are a hashed bag of words, not a language model. They exist so
// the example can run without downloading one. Swap embed for a real model
// and keep the same REST calls.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"hash/fnv"
	"io"
	"net/http"
	"os"
	"strings"
)

const dim = 64

func main() {
	base := flag.String("http", "http://127.0.0.1:7879", "ProximaDB REST base URL")
	flag.Parse()
	hits, err := run(strings.TrimRight(*base, "/"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("query: red shoes")
	fmt.Println("filter: color=red")
	for i, hit := range hits {
		fmt.Printf("%d  %.3f  %s\n", i+1, hit.Distance, hit.Text)
	}
}

type item struct {
	text string
	tags map[string]string
}

type hit struct {
	Text     string
	Distance float32
}

func run(base string) ([]hit, error) {
	if err := post(base+"/v1/collections", map[string]any{
		"name":      "catalog",
		"dimension": dim,
		"metric":    "METRIC_COSINE",
	}); err != nil {
		return nil, err
	}
	for i, it := range catalog {
		if err := post(fmt.Sprintf("%s/v1/collections/catalog/vectors", base), map[string]any{
			"id":     fmt.Sprintf("%d", i+1),
			"vector": embed(it.text),
			"tags":   it.tags,
		}); err != nil {
			return nil, err
		}
	}
	var resp struct {
		Hits []struct {
			ID       string  `json:"id"`
			Distance float32 `json:"distance"`
		} `json:"hits"`
	}
	body, err := postJSON(base+"/v1/collections/catalog/search", map[string]any{
		"vector": embed("red shoes"),
		"k":      3,
		"filter": map[string]string{"color": "red"},
	})
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	byID := make(map[string]string, len(catalog))
	for i, it := range catalog {
		byID[fmt.Sprintf("%d", i+1)] = it.text
	}
	hits := make([]hit, len(resp.Hits))
	for i, got := range resp.Hits {
		hits[i] = hit{Text: byID[got.ID], Distance: got.Distance}
	}
	return hits, nil
}

func post(url string, payload any) error {
	_, err := postJSON(url, payload)
	return err
}

func postJSON(url string, payload any) ([]byte, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	res, err := http.Post(url, "application/json", bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("demo: %s: %w", url, err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("demo: %s status %d: %s", url, res.StatusCode, bytes.TrimSpace(body))
	}
	return body, nil
}

// embed counts each distinct word into a fixed-size vector.
func embed(text string) []float32 {
	vec := make([]float32, dim)
	for _, tok := range tokens(text) {
		h := fnv.New32a()
		_, _ = h.Write([]byte(tok))
		vec[h.Sum32()%uint32(dim)]++
	}
	return vec
}

func tokens(text string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, raw := range strings.Fields(strings.ToLower(text)) {
		tok := strings.Trim(raw, ".,;:!?\"'")
		if tok == "" {
			continue
		}
		if _, ok := seen[tok]; ok {
			continue
		}
		seen[tok] = struct{}{}
		out = append(out, tok)
	}
	return out
}

var catalog = []item{
	{"red leather shoes", map[string]string{"color": "red"}},
	{"red canvas sneakers", map[string]string{"color": "red"}},
	{"bright red wool coat", map[string]string{"color": "red"}},
	{"red ceramic mug", map[string]string{"color": "red"}},
	{"scarlet silk scarf", map[string]string{"color": "red"}},
	{"blue running shoes", map[string]string{"color": "blue"}},
	{"navy wool coat", map[string]string{"color": "blue"}},
	{"blue ceramic mug", map[string]string{"color": "blue"}},
	{"black leather boots", map[string]string{"color": "black"}},
	{"white cotton shirt", map[string]string{"color": "white"}},
	{"green hiking boots", map[string]string{"color": "green"}},
	{"yellow rain jacket", map[string]string{"color": "yellow"}},
}
