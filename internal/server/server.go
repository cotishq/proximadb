package server

import (
	"context"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otelcodes "go.opentelemetry.io/otel/codes"
	otelmetric "go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
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
	store    *collection.Store
	tracer   trace.Tracer
	requests otelmetric.Int64Counter
	duration otelmetric.Float64Histogram
}

// New returns a server for store.
func New(store *collection.Store) *Server {
	meter := otel.Meter("github.com/cotishq/proximadb/internal/server")
	requests, err := meter.Int64Counter(
		"proxima.requests",
		otelmetric.WithDescription("RPCs by method"),
	)
	if err != nil {
		panic(err)
	}
	duration, err := meter.Float64Histogram(
		"proxima.request.duration",
		otelmetric.WithUnit("s"),
		otelmetric.WithDescription("RPC duration in seconds"),
		otelmetric.WithExplicitBucketBoundaries(0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5),
	)
	if err != nil {
		panic(err)
	}
	_, err = meter.Int64ObservableGauge(
		"proxima.index.size",
		otelmetric.WithDescription("Live vectors across collections"),
		otelmetric.WithInt64Callback(func(_ context.Context, o otelmetric.Int64Observer) error {
			o.Observe(int64(store.LiveCount()))
			return nil
		}),
	)
	if err != nil {
		panic(err)
	}
	return &Server{
		store:    store,
		tracer:   otel.Tracer("github.com/cotishq/proximadb/internal/server"),
		requests: requests,
		duration: duration,
	}
}

// CreateCollection registers a named collection.
func (s *Server) CreateCollection(ctx context.Context, req *proximadbv1.CreateCollectionRequest) (resp *proximadbv1.CreateCollectionResponse, err error) {
	ctx, done := s.track(ctx, "CreateCollection")
	defer func() { done(err) }()

	if err = ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}
	m, err := toMetric(req.GetMetric())
	if err != nil {
		return nil, err
	}
	if err = s.store.Create(req.GetName(), m, int(req.GetDimension())); err != nil {
		return nil, rpcErr(err)
	}
	return &proximadbv1.CreateCollectionResponse{}, nil
}

// Insert stores one vector and its tags.
func (s *Server) Insert(ctx context.Context, req *proximadbv1.InsertRequest) (resp *proximadbv1.InsertResponse, err error) {
	ctx, done := s.track(ctx, "Insert")
	defer func() { done(err) }()

	if err = ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}
	if err = s.store.Insert(req.GetCollection(), req.GetId(), req.GetVector(), req.GetTags()); err != nil {
		return nil, rpcErr(err)
	}
	return &proximadbv1.InsertResponse{}, nil
}

// Search returns the k closest vectors in one collection.
func (s *Server) Search(ctx context.Context, req *proximadbv1.SearchRequest) (resp *proximadbv1.SearchResponse, err error) {
	ctx, done := s.track(ctx, "Search")
	defer func() { done(err) }()

	if err = ctx.Err(); err != nil {
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
func (s *Server) Delete(ctx context.Context, req *proximadbv1.DeleteRequest) (resp *proximadbv1.DeleteResponse, err error) {
	ctx, done := s.track(ctx, "Delete")
	defer func() { done(err) }()

	if err = ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}
	if err = s.store.Delete(req.GetCollection(), req.GetId()); err != nil {
		return nil, rpcErr(err)
	}
	return &proximadbv1.DeleteResponse{}, nil
}

func (s *Server) track(ctx context.Context, rpc string) (context.Context, func(error)) {
	ctx, span := s.tracer.Start(ctx, rpc)
	start := time.Now()
	return ctx, func(err error) {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(otelcodes.Error, err.Error())
		}
		attrs := otelmetric.WithAttributes(attribute.String("rpc", rpc))
		s.requests.Add(ctx, 1, attrs)
		s.duration.Record(ctx, time.Since(start).Seconds(), attrs)
		span.End()
	}
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
