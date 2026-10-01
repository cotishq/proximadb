package main

import (
	"flag"
	"log"
	"net"
	"os"

	"google.golang.org/grpc"

	"github.com/cotishq/proximadb/gen/proximadbv1"
	"github.com/cotishq/proximadb/internal/collection"
	"github.com/cotishq/proximadb/internal/server"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:7878", "address to serve gRPC on")
	data := flag.String("data", "", "directory for the write-ahead log")
	flag.Parse()
	if *data == "" {
		log.Fatal("proximadb: -data directory is required")
	}

	store, err := collection.Open(*data)
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()

	lis, err := net.Listen("tcp", *listen)
	if err != nil {
		log.Fatal(err)
	}
	gs := grpc.NewServer()
	proximadbv1.RegisterProximaServer(gs, server.New(store))
	log.Printf("proximadb listening on %s", lis.Addr())
	if err := gs.Serve(lis); err != nil {
		log.Println(err)
		os.Exit(1)
	}
}
