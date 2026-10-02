package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"

	"github.com/cotishq/proximadb/gen/proximadbv1"
	"github.com/cotishq/proximadb/internal/collection"
)

func TestRESTInsertAndSearch(t *testing.T) {
	store, err := collection.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	mux := runtime.NewServeMux()
	if err := proximadbv1.RegisterProximaHandlerServer(context.Background(), mux, New(store)); err != nil {
		t.Fatal(err)
	}
	httpSrv := httptest.NewServer(mux)
	defer httpSrv.Close()

	post(t, httpSrv.URL+"/v1/collections", `{"name":"images","metric":"METRIC_L2","dimension":2}`, http.StatusOK)
	post(t, httpSrv.URL+"/v1/collections/images/vectors", `{"id":7,"vector":[1,0],"tags":{"color":"red"}}`, http.StatusOK)

	body := post(t, httpSrv.URL+"/v1/collections/images/search", `{"vector":[1,0],"k":1,"filter":{"color":"red"}}`, http.StatusOK)
	var resp struct {
		Hits []struct {
			ID       string  `json:"id"`
			Distance float32 `json:"distance"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Hits) != 1 || resp.Hits[0].ID != "7" || resp.Hits[0].Distance != 0 {
		t.Fatalf("hits = %+v, want id 7 at distance 0", resp.Hits)
	}
}

func post(t *testing.T, url, jsonBody string, want int) []byte {
	t.Helper()
	res, err := http.Post(url, "application/json", bytes.NewBufferString(jsonBody))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != want {
		t.Fatalf("POST %s status = %d, body = %s", url, res.StatusCode, body)
	}
	return body
}
