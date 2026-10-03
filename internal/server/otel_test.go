package server

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/cotishq/proximadb/gen/proximadbv1"
	"github.com/cotishq/proximadb/internal/collection"
	"github.com/cotishq/proximadb/internal/metric"
)

func TestSearchRecordsSpanAndCounter(t *testing.T) {
	ctx := context.Background()

	prevTracer := otel.GetTracerProvider()
	prevMeter := otel.GetMeterProvider()
	t.Cleanup(func() {
		otel.SetTracerProvider(prevTracer)
		otel.SetMeterProvider(prevMeter)
	})

	rec := tracetest.NewSpanRecorder()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)))
	reader := sdkmetric.NewManualReader()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))

	store := collection.New()
	if err := store.Create("images", metric.L2, 2); err != nil {
		t.Fatal(err)
	}
	if err := store.Insert("images", 7, []float32{1, 0}, nil); err != nil {
		t.Fatal(err)
	}
	srv := New(store)

	_, err := srv.Search(ctx, &proximadbv1.SearchRequest{
		Collection: "images",
		Vector:     []float32{1, 0},
		K:          1,
	})
	if err != nil {
		t.Fatal(err)
	}

	spans := rec.Ended()
	if len(spans) != 1 || spans[0].Name() != "Search" {
		t.Fatalf("spans = %+v", spans)
	}

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &rm); err != nil {
		t.Fatal(err)
	}
	if got, ok := counterValue(rm, "proxima.requests"); !ok || got != 1 {
		t.Fatalf("proxima.requests = %d, ok %v", got, ok)
	}
	if got, ok := gaugeValue(rm, "proxima.index.size"); !ok || got != 1 {
		t.Fatalf("proxima.index.size = %d, ok %v", got, ok)
	}
}

func counterValue(rm metricdata.ResourceMetrics, name string) (int64, bool) {
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok {
				return 0, false
			}
			var n int64
			for _, dp := range sum.DataPoints {
				n += dp.Value
			}
			return n, true
		}
	}
	return 0, false
}

func gaugeValue(rm metricdata.ResourceMetrics, name string) (int64, bool) {
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			gauge, ok := m.Data.(metricdata.Gauge[int64])
			if !ok {
				return 0, false
			}
			var n int64
			for _, dp := range gauge.DataPoints {
				n += dp.Value
			}
			return n, true
		}
	}
	return 0, false
}
