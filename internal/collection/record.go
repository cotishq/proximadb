package collection

import (
	"encoding/binary"
	"fmt"
	"math"

	"github.com/cotishq/proximadb/internal/metric"
)

const (
	opCreate byte = 1
	opInsert byte = 2
	opDelete byte = 3
)

func encodeCreate(name string, m metric.Metric, dim int) []byte {
	buf := make([]byte, 0, 16+len(name))
	buf = append(buf, opCreate)
	buf = putString(buf, name)
	buf = append(buf, byte(m))
	buf = putU32(buf, uint32(dim))
	return buf
}

func encodeInsert(name string, id uint64, vec []float32, tags map[string]string) []byte {
	buf := make([]byte, 0, 32+len(name)+len(vec)*4)
	buf = append(buf, opInsert)
	buf = putString(buf, name)
	buf = putU64(buf, id)
	buf = putU32(buf, uint32(len(vec)))
	for _, v := range vec {
		buf = putU32(buf, math.Float32bits(v))
	}
	buf = putU32(buf, uint32(len(tags)))
	for key, value := range tags {
		buf = putString(buf, key)
		buf = putString(buf, value)
	}
	return buf
}

func encodeDelete(name string, id uint64) []byte {
	buf := make([]byte, 0, 16+len(name))
	buf = append(buf, opDelete)
	buf = putString(buf, name)
	buf = putU64(buf, id)
	return buf
}

func decodeCreate(payload []byte) (string, metric.Metric, int, error) {
	r := reader{b: payload}
	op, err := r.u8()
	if err != nil {
		return "", 0, 0, err
	}
	if op != opCreate {
		return "", 0, 0, fmt.Errorf("collection: want create op")
	}
	name, err := r.str()
	if err != nil {
		return "", 0, 0, err
	}
	mb, err := r.u8()
	if err != nil {
		return "", 0, 0, err
	}
	dim, err := r.u32()
	if err != nil {
		return "", 0, 0, err
	}
	if err := r.done(); err != nil {
		return "", 0, 0, err
	}
	return name, metric.Metric(mb), int(dim), nil
}

func decodeInsert(payload []byte) (string, uint64, []float32, map[string]string, error) {
	r := reader{b: payload}
	op, err := r.u8()
	if err != nil {
		return "", 0, nil, nil, err
	}
	if op != opInsert {
		return "", 0, nil, nil, fmt.Errorf("collection: want insert op")
	}
	name, err := r.str()
	if err != nil {
		return "", 0, nil, nil, err
	}
	id, err := r.u64()
	if err != nil {
		return "", 0, nil, nil, err
	}
	n, err := r.u32()
	if err != nil {
		return "", 0, nil, nil, err
	}
	vec := make([]float32, n)
	for i := range vec {
		bits, err := r.u32()
		if err != nil {
			return "", 0, nil, nil, err
		}
		vec[i] = math.Float32frombits(bits)
	}
	nt, err := r.u32()
	if err != nil {
		return "", 0, nil, nil, err
	}
	var tags map[string]string
	if nt > 0 {
		tags = make(map[string]string, nt)
	}
	for i := uint32(0); i < nt; i++ {
		key, err := r.str()
		if err != nil {
			return "", 0, nil, nil, err
		}
		value, err := r.str()
		if err != nil {
			return "", 0, nil, nil, err
		}
		tags[key] = value
	}
	if err := r.done(); err != nil {
		return "", 0, nil, nil, err
	}
	return name, id, vec, tags, nil
}

func decodeDelete(payload []byte) (string, uint64, error) {
	r := reader{b: payload}
	op, err := r.u8()
	if err != nil {
		return "", 0, err
	}
	if op != opDelete {
		return "", 0, fmt.Errorf("collection: want delete op")
	}
	name, err := r.str()
	if err != nil {
		return "", 0, err
	}
	id, err := r.u64()
	if err != nil {
		return "", 0, err
	}
	if err := r.done(); err != nil {
		return "", 0, err
	}
	return name, id, nil
}

func putU32(buf []byte, v uint32) []byte {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], v)
	return append(buf, b[:]...)
}

func putU64(buf []byte, v uint64) []byte {
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], v)
	return append(buf, b[:]...)
}

func putString(buf []byte, s string) []byte {
	buf = putU32(buf, uint32(len(s)))
	return append(buf, s...)
}

type reader struct {
	b []byte
	i int
}

func (r *reader) u8() (byte, error) {
	if r.i >= len(r.b) {
		return 0, fmt.Errorf("collection: short log record")
	}
	v := r.b[r.i]
	r.i++
	return v, nil
}

func (r *reader) u32() (uint32, error) {
	if r.i+4 > len(r.b) {
		return 0, fmt.Errorf("collection: short log record")
	}
	v := binary.LittleEndian.Uint32(r.b[r.i : r.i+4])
	r.i += 4
	return v, nil
}

func (r *reader) u64() (uint64, error) {
	if r.i+8 > len(r.b) {
		return 0, fmt.Errorf("collection: short log record")
	}
	v := binary.LittleEndian.Uint64(r.b[r.i : r.i+8])
	r.i += 8
	return v, nil
}

func (r *reader) str() (string, error) {
	n, err := r.u32()
	if err != nil {
		return "", err
	}
	if r.i+int(n) > len(r.b) {
		return "", fmt.Errorf("collection: short log record")
	}
	s := string(r.b[r.i : r.i+int(n)])
	r.i += int(n)
	return s, nil
}

func (r *reader) done() error {
	if r.i != len(r.b) {
		return fmt.Errorf("collection: trailing bytes in log record")
	}
	return nil
}
