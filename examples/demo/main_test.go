package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"

	"github.com/cotishq/proximadb/gen/proximadbv1"
	"github.com/cotishq/proximadb/internal/collection"
	"github.com/cotishq/proximadb/internal/server"
)

func TestDemoFindsRedShoes(t *testing.T) {
	store, err := collection.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	mux := runtime.NewServeMux()
	if err := proximadbv1.RegisterProximaHandlerServer(context.Background(), mux, server.New(store)); err != nil {
		t.Fatal(err)
	}
	httpSrv := httptest.NewServer(mux)
	defer httpSrv.Close()

	hits, err := run(httpSrv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 3 {
		t.Fatalf("len = %d, want 3", len(hits))
	}
	if hits[0].Text != "red leather shoes" {
		t.Fatalf("top = %q, want red leather shoes", hits[0].Text)
	}
	allowed := map[string]struct{}{
		"red leather shoes":    {},
		"red canvas sneakers":  {},
		"bright red wool coat": {},
		"red ceramic mug":      {},
		"scarlet silk scarf":   {},
	}
	for _, hit := range hits {
		if _, ok := allowed[hit.Text]; !ok {
			t.Fatalf("filtered hit %q is not tagged red", hit.Text)
		}
	}
}

func TestDemoRequiresServer(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	if _, err := run(srv.URL); err == nil {
		t.Fatal("closed server must fail")
	}
}
