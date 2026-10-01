package server

import (
	"context"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/cotishq/proximadb/gen/proximadbv1"
	"github.com/cotishq/proximadb/internal/collection"
	"github.com/cotishq/proximadb/internal/metric"
)

// Server serves the collection store over gRPC.
// It does not know how the index finds neighbors.
type Server struct {
	proximadbv1.UnimplementedProximaServer
	store *collection.Store
}

// New returns a server for store.
func New(store *collection.Store) *Server {
	return &Server{store: store}
}

// CreateCollection registers a named collection.
func (s *Server) CreateCollection(ctx context.Context, req *proximadbv1.CreateCollectionRequest) (*proximadbv1.CreateCollectionResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}
	m, err := toMetric(req.GetMetric())
	if err != nil {
		return nil, err
	}
	if err := s.store.Create(req.GetName(), m, int(req.GetDimension())); err != nil {
		return nil, rpcErr(err)
	}
	return &proximadbv1.CreateCollectionResponse{}, nil
}

// Insert stores one vector and its tags.
func (s *Server) Insert(ctx context.Context, req *proximadbv1.InsertRequest) (*proximadbv1.InsertResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}
	if err := s.store.Insert(req.GetCollection(), req.GetId(), req.GetVector(), req.GetTags()); err != nil {
		return nil, rpcErr(err)
	}
	return &proximadbv1.InsertResponse{}, nil
}

// Search returns the k closest vectors in one collection.
func (s *Server) Search(ctx context.Context, req *proximadbv1.SearchRequest) (*proximadbv1.SearchResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}
	hits, err := s.store.Search(req.GetCollection(), req.GetVector(), int(req.GetK()), req.GetFilter())
	if err != nil {
		return nil, rpcErr(err)
	}
	out := &proximadbv1.SearchResponse{Hits: make([]*proximadbv1.Hit, len(hits))}
	for i, hit := range hits {
		out.Hits[i] = &proximadbv1.Hit{Id: hit.ID, Distance: hit.Distance}
	}
	return out, nil
}

// Delete tombstones one id.
func (s *Server) Delete(ctx context.Context, req *proximadbv1.DeleteRequest) (*proximadbv1.DeleteResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}
	if err := s.store.Delete(req.GetCollection(), req.GetId()); err != nil {
		return nil, rpcErr(err)
	}
	return &proximadbv1.DeleteResponse{}, nil
}

func toMetric(m proximadbv1.Metric) (metric.Metric, error) {
	switch m {
	case proximadbv1.Metric_METRIC_COSINE:
		return metric.Cosine, nil
	case proximadbv1.Metric_METRIC_L2:
		return metric.L2, nil
	case proximadbv1.Metric_METRIC_DOT:
		return metric.Dot, nil
	default:
		return 0, status.Error(codes.InvalidArgument, "metric is required")
	}
}

func rpcErr(err error) error {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "not found"):
		return status.Error(codes.NotFound, msg)
	case strings.Contains(msg, "already exists"):
		return status.Error(codes.AlreadyExists, msg)
	case strings.Contains(msg, "wal:"):
		return status.Error(codes.Internal, msg)
	default:
		return status.Error(codes.InvalidArgument, msg)
	}
}
