package main

import (
	"context"
	"flag"
	"log"
	"net"
	"net/http"
	"os"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc"

	"github.com/cotishq/proximadb/gen/proximadbv1"
	"github.com/cotishq/proximadb/internal/collection"
	"github.com/cotishq/proximadb/internal/server"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:7878", "address to serve gRPC on")
	httpListen := flag.String("http", "127.0.0.1:7879", "address to serve REST on")
	data := flag.String("data", "", "directory for the write-ahead log")
	flag.Parse()
	if *data == "" {
		log.Fatal("proximadb: -data directory is required")
	}

	shutdown, err := setupOTelSDK(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	defer shutdown(context.Background())

	store, err := collection.Open(*data)
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()

	srv := server.New(store)
	gs := grpc.NewServer()
	proximadbv1.RegisterProximaServer(gs, srv)

	mux := runtime.NewServeMux()
	if err := proximadbv1.RegisterProximaHandlerServer(context.Background(), mux, srv); err != nil {
		log.Fatal(err)
	}
	root := http.NewServeMux()
	root.Handle("/metrics", promhttp.Handler())
	root.Handle("/", mux)

	grpcLis, err := net.Listen("tcp", *listen)
	if err != nil {
		log.Fatal(err)
	}
	httpLis, err := net.Listen("tcp", *httpListen)
	if err != nil {
		log.Fatal(err)
	}
	go func() {
		log.Printf("proximadb REST listening on %s", httpLis.Addr())
		if err := http.Serve(httpLis, root); err != nil {
			log.Println(err)
			os.Exit(1)
		}
	}()
	log.Printf("proximadb gRPC listening on %s", grpcLis.Addr())
	if err := gs.Serve(grpcLis); err != nil {
		log.Println(err)
		os.Exit(1)
	}
}
