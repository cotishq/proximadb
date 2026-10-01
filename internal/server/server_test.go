package server

import (
	"context"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"github.com/cotishq/proximadb/gen/proximadbv1"
	"github.com/cotishq/proximadb/internal/collection"
)

func TestClientInsertAndSearch(t *testing.T) {
	dir := t.TempDir()
	store, err := collection.Open(dir)
	if err != nil {
		t.Fatal(err)
	}

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	gs := grpc.NewServer()
	proximadbv1.RegisterProximaServer(gs, New(store))
	go gs.Serve(lis)

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	client := proximadbv1.NewProximaClient(conn)
	ctx := context.Background()

	_, err = client.CreateCollection(ctx, &proximadbv1.CreateCollectionRequest{
		Name:      "images",
		Metric:    proximadbv1.Metric_METRIC_L2,
		Dimension: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Insert(ctx, &proximadbv1.InsertRequest{
		Collection: "images",
		Id:         7,
		Vector:     []float32{1, 0},
		Tags:       map[string]string{"color": "red"},
	})
	if err != nil {
		t.Fatal(err)
	}

	resp, err := client.Search(ctx, &proximadbv1.SearchRequest{
		Collection: "images",
		Vector:     []float32{1, 0},
		K:          1,
		Filter:     map[string]string{"color": "red"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.GetHits()) != 1 || resp.GetHits()[0].GetId() != 7 || resp.GetHits()[0].GetDistance() != 0 {
		t.Fatalf("hits = %+v, want id 7 at distance 0", resp.GetHits())
	}

	_, err = client.Delete(ctx, &proximadbv1.DeleteRequest{Collection: "images", Id: 7})
	if err != nil {
		t.Fatal(err)
	}
	missing, err := client.Search(ctx, &proximadbv1.SearchRequest{
		Collection: "images",
		Vector:     []float32{1, 0},
		K:          1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(missing.GetHits()) != 0 {
		t.Fatalf("deleted id returned %+v", missing.GetHits())
	}

	_, err = client.CreateCollection(ctx, &proximadbv1.CreateCollectionRequest{
		Name:      "images",
		Metric:    proximadbv1.Metric_METRIC_COSINE,
		Dimension: 2,
	})
	if status.Code(err) != codes.AlreadyExists {
		t.Fatalf("duplicate create status = %v, want AlreadyExists", status.Code(err))
	}

	gs.GracefulStop()
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := collection.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	hits, err := reopened.Search("images", []float32{1, 0}, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("replayed hits = %+v, want the deleted id to stay gone", hits)
	}
	if err := reopened.Insert("images", 8, []float32{0, 1}, nil); err != nil {
		t.Fatal(err)
	}
}
