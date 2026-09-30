package wal

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"sync"
)

const (
	magic      = "PXW1"
	headerSize = 4
	maxPayload = 32 << 20
)

// Log is an append-only file of checksummed records.
// A torn tail is dropped the next time the file is opened.
type Log struct {
	mu   sync.Mutex
	f    *os.File
	recs [][]byte
}

// Open creates or reopens the log at path.
func Open(path string) (*Log, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	l := &Log{f: f}
	if err := l.init(); err != nil {
		f.Close()
		return nil, err
	}
	return l, nil
}

func (l *Log) init() error {
	info, err := l.f.Stat()
	if err != nil {
		return err
	}
	if info.Size() == 0 {
		if _, err := l.f.Write([]byte(magic)); err != nil {
			return err
		}
		if err := l.f.Sync(); err != nil {
			return err
		}
		return syncDir(l.f.Name())
	}

	hdr := make([]byte, headerSize)
	if _, err := l.f.ReadAt(hdr, 0); err != nil {
		return fmt.Errorf("wal: read header: %w", err)
	}
	if string(hdr) != magic {
		return fmt.Errorf("wal: bad header")
	}

	off := int64(headerSize)
	for {
		var meta [8]byte
		_, err := l.f.ReadAt(meta[:], off)
		if err != nil {
			break
		}
		n := binary.LittleEndian.Uint32(meta[0:4])
		sum := binary.LittleEndian.Uint32(meta[4:8])
		if n == 0 || n > maxPayload {
			break
		}
		payload := make([]byte, n)
		if _, err := l.f.ReadAt(payload, off+8); err != nil {
			break
		}
		if crc32.ChecksumIEEE(payload) != sum {
			break
		}
		l.recs = append(l.recs, payload)
		off += 8 + int64(n)
	}
	if err := l.f.Truncate(off); err != nil {
		return err
	}
	_, err = l.f.Seek(off, io.SeekStart)
	return err
}

// Append writes one record and syncs it to disk before returning.
func (l *Log) Append(payload []byte) error {
	if len(payload) == 0 || len(payload) > maxPayload {
		return fmt.Errorf("wal: bad payload length %d", len(payload))
	}
	frame := make([]byte, 8+len(payload))
	binary.LittleEndian.PutUint32(frame[0:4], uint32(len(payload)))
	binary.LittleEndian.PutUint32(frame[4:8], crc32.ChecksumIEEE(payload))
	copy(frame[8:], payload)

	l.mu.Lock()
	defer l.mu.Unlock()
	if _, err := l.f.Write(frame); err != nil {
		return err
	}
	if err := l.f.Sync(); err != nil {
		return err
	}
	copied := make([]byte, len(payload))
	copy(copied, payload)
	l.recs = append(l.recs, copied)
	return nil
}

// Records returns the records that survived the last open, plus later appends.
func (l *Log) Records() [][]byte {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([][]byte, len(l.recs))
	copy(out, l.recs)
	return out
}

// Close closes the file.
func (l *Log) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return nil
	}
	err := l.f.Close()
	l.f = nil
	return err
}

func syncDir(path string) error {
	d, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
