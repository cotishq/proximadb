package wal

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTornTailIsDropped(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wal.log")

	log, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := log.Append([]byte("one")); err != nil {
		t.Fatal(err)
	}
	if err := log.Append([]byte("two")); err != nil {
		t.Fatal(err)
	}
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte{0x01, 0x02, 0x03}); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	log, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	recs := log.Records()
	if len(recs) != 2 || string(recs[0]) != "one" || string(recs[1]) != "two" {
		t.Fatalf("records = %q, want one two", recs)
	}
	if err := log.Append([]byte("three")); err != nil {
		t.Fatal(err)
	}
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}

	log, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	recs = log.Records()
	if len(recs) != 3 || string(recs[2]) != "three" {
		t.Fatalf("records = %q, want three records ending in three", recs)
	}
}
