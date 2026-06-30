package segment

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// A segment is an immutable, time-bounded batch of events stored column-oriented and
// flate-compressed — the unit of the warm/cold tiers. Columnar layout means a query
// reading only some fields decompresses less, dictionary-encoding collapses the many
// repeated event names / user ids, and the whole thing compresses far better than the
// row-wise JSONL log. stdlib only — no CGO, no Parquet dependency (Parquet stays an
// export format for warehouse interop, not the hot path).

var magic = [4]byte{'S', 'M', 'S', '1'}

// encodeSegment serializes events into one compressed columnar blob and returns it with
// the segment's min/max timestamp and the distinct event names (for the manifest index).
func encodeSegment(evs []event.Event) (data []byte, minTS, maxTS time.Time, names []string, err error) {
	var raw bytes.Buffer
	raw.Write(magic[:])
	writeU32(&raw, uint32(len(evs)))

	// timestamps column + min/max
	minN, maxN := int64(0), int64(0)
	for i, e := range evs {
		n := e.Timestamp.UnixNano()
		if i == 0 || n < minN {
			minN = n
		}
		if i == 0 || n > maxN {
			maxN = n
		}
	}
	writeI64(&raw, minN)
	writeI64(&raw, maxN)
	for _, e := range evs {
		writeI64(&raw, e.Timestamp.UnixNano())
	}

	// dictionary columns: name, distinct_id
	nameDict := writeDictColumn(&raw, evs, func(e event.Event) string { return e.Name })
	writeDictColumn(&raw, evs, func(e event.Event) string { return e.DistinctID })

	// high-cardinality columns: id, properties(JSON)
	for _, e := range evs {
		writeStr(&raw, e.ID)
	}
	for _, e := range evs {
		if len(e.Properties) == 0 {
			writeBytes(&raw, nil)
			continue
		}
		b, mErr := json.Marshal(e.Properties)
		if mErr != nil {
			return nil, time.Time{}, time.Time{}, nil, mErr
		}
		writeBytes(&raw, b)
	}

	// frame with a CRC32 so a truncated / bit-rotted segment is a clean error on read,
	// not silently-decoded garbage; then flate-compress the whole thing.
	body := raw.Bytes()
	framed := make([]byte, 4, len(body)+4)
	binary.LittleEndian.PutUint32(framed, crc32.ChecksumIEEE(body))
	framed = append(framed, body...)
	var comp bytes.Buffer
	fw, ferr := flate.NewWriter(&comp, flate.BestCompression)
	if ferr != nil {
		return nil, time.Time{}, time.Time{}, nil, ferr
	}
	if _, err = fw.Write(framed); err != nil {
		_ = fw.Close()
		return nil, time.Time{}, time.Time{}, nil, err
	}
	if err = fw.Close(); err != nil {
		return nil, time.Time{}, time.Time{}, nil, err
	}

	mn, mx := time.Time{}, time.Time{}
	if len(evs) > 0 {
		mn, mx = time.Unix(0, minN).UTC(), time.Unix(0, maxN).UTC()
	}
	return comp.Bytes(), mn, mx, nameDict, nil
}

// decodeSegment reverses encodeSegment back into events (in stored order).
func decodeSegment(data []byte) ([]event.Event, error) {
	fr := flate.NewReader(bytes.NewReader(data))
	raw, err := io.ReadAll(fr)
	_ = fr.Close()
	if err != nil {
		return nil, err
	}
	if len(raw) < 4 {
		return nil, fmt.Errorf("segment: truncated blob")
	}
	want := binary.LittleEndian.Uint32(raw[:4])
	raw = raw[4:]
	if crc32.ChecksumIEEE(raw) != want {
		return nil, fmt.Errorf("segment: checksum mismatch — corrupt blob")
	}
	r := bytes.NewReader(raw)

	var m [4]byte
	if _, err := io.ReadFull(r, m[:]); err != nil {
		return nil, err
	}
	if m != magic {
		return nil, fmt.Errorf("segment: bad magic")
	}
	n := int(readU32(r))
	if n < 0 {
		return nil, fmt.Errorf("segment: bad count")
	}
	evs := make([]event.Event, n)

	_ = readI64(r) // minTS (already in the manifest)
	_ = readI64(r) // maxTS
	for i := 0; i < n; i++ {
		evs[i].Timestamp = time.Unix(0, readI64(r)).UTC()
	}
	names, err := readDictColumn(r, n)
	if err != nil {
		return nil, err
	}
	dids, err := readDictColumn(r, n)
	if err != nil {
		return nil, err
	}
	for i := 0; i < n; i++ {
		evs[i].Name = names[i]
		evs[i].DistinctID = dids[i]
	}
	for i := 0; i < n; i++ {
		evs[i].ID = readStr(r)
	}
	for i := 0; i < n; i++ {
		b := readBytes(r)
		if len(b) > 0 {
			var p map[string]any
			if err := json.Unmarshal(b, &p); err != nil {
				return nil, err
			}
			evs[i].Properties = p
		}
	}
	return evs, nil
}

// --- dictionary column: distinct values once, then a uint32 index per row ---

func writeDictColumn(w *bytes.Buffer, evs []event.Event, get func(event.Event) string) []string {
	idx := make(map[string]uint32)
	var dict []string
	indices := make([]uint32, len(evs))
	for i, e := range evs {
		v := get(e)
		id, ok := idx[v]
		if !ok {
			id = uint32(len(dict))
			idx[v] = id
			dict = append(dict, v)
		}
		indices[i] = id
	}
	writeU32(w, uint32(len(dict)))
	for _, s := range dict {
		writeStr(w, s)
	}
	for _, id := range indices {
		writeU32(w, id)
	}
	return dict
}

func readDictColumn(r *bytes.Reader, n int) ([]string, error) {
	dn := int(readU32(r))
	if dn < 0 {
		return nil, fmt.Errorf("segment: bad dict size")
	}
	dict := make([]string, dn)
	for i := 0; i < dn; i++ {
		dict[i] = readStr(r)
	}
	out := make([]string, n)
	for i := 0; i < n; i++ {
		id := int(readU32(r))
		if id < 0 || id >= dn {
			return nil, fmt.Errorf("segment: dict index out of range")
		}
		out[i] = dict[id]
	}
	return out, nil
}

// --- primitive little-endian readers/writers ---

func writeU32(w *bytes.Buffer, v uint32) { _ = binary.Write(w, binary.LittleEndian, v) }
func writeI64(w *bytes.Buffer, v int64)  { _ = binary.Write(w, binary.LittleEndian, v) }

func writeStr(w *bytes.Buffer, s string) { writeBytes(w, []byte(s)) }
func writeBytes(w *bytes.Buffer, b []byte) {
	writeU32(w, uint32(len(b)))
	w.Write(b)
}

func readU32(r *bytes.Reader) uint32 {
	var v uint32
	_ = binary.Read(r, binary.LittleEndian, &v)
	return v
}
func readI64(r *bytes.Reader) int64 {
	var v int64
	_ = binary.Read(r, binary.LittleEndian, &v)
	return v
}
func readStr(r *bytes.Reader) string { return string(readBytes(r)) }
func readBytes(r *bytes.Reader) []byte {
	n := int(readU32(r))
	if n <= 0 {
		return nil
	}
	b := make([]byte, n)
	_, _ = io.ReadFull(r, b)
	return b
}
