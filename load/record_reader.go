package load

import (
	"bufio"
	"compress/gzip"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync/atomic"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
	"github.com/apache/arrow-go/v18/arrow/memory"
	geoarrow "github.com/geoarrow/geoarrow-go"
	"github.com/twpayne/go-geom/encoding/wkb"
	"github.com/twpayne/go-geom/encoding/wkt"
)

const geoSPARQLWKTLiteral = "http://www.opengis.net/ont/geosparql#wktLiteral"

type nquadRecordReader struct {
	refCount atomic.Int64

	schema    *arrow.Schema
	pool      memory.Allocator
	paths     []string
	batchSize int

	fileIndex int
	file      *os.File
	gz        *gzip.Reader
	br        *bufio.Reader
	lineNum   int

	current arrow.RecordBatch
	err     error
	rows    int64
}

func newNQuadRecordReader(paths []string, schema *arrow.Schema, batchSize int) *nquadRecordReader {
	r := &nquadRecordReader{
		schema:    schema,
		pool:      memory.NewGoAllocator(),
		paths:     paths,
		batchSize: batchSize,
	}
	r.refCount.Store(1)
	return r
}

func (r *nquadRecordReader) Retain() {
	r.refCount.Add(1)
}

func (r *nquadRecordReader) Release() {
	if r.refCount.Add(-1) != 0 {
		return
	}
	r.releaseCurrent()
	r.closeCurrentFile()
}

func (r *nquadRecordReader) Schema() *arrow.Schema {
	return r.schema
}

func (r *nquadRecordReader) Next() bool {
	r.releaseCurrent()
	if r.err != nil {
		return false
	}

	rec, err := r.nextBatch()
	if err != nil {
		r.err = err
		return false
	}
	r.current = rec
	return rec != nil
}

func (r *nquadRecordReader) RecordBatch() arrow.RecordBatch {
	return r.current
}

func (r *nquadRecordReader) Record() arrow.RecordBatch {
	return r.RecordBatch()
}

func (r *nquadRecordReader) Err() error {
	return r.err
}

func (r *nquadRecordReader) RowsRead() int64 {
	return r.rows
}

func (r *nquadRecordReader) nextBatch() (arrow.RecordBatch, error) {
	builder := array.NewRecordBuilder(r.pool, r.schema)
	defer builder.Release()

	count := 0
	for count < r.batchSize {
		t, ok, err := r.nextTriple()
		if err != nil {
			return nil, err
		}
		if !ok {
			break
		}
		builder.Field(0).(*array.StringBuilder).Append(t.s)
		builder.Field(1).(*array.StringBuilder).Append(t.p)
		if err := appendObjectFields(builder, t); err != nil {
			return nil, fmt.Errorf("serialize object for %s %s: %w", t.s, t.p, err)
		}
		count++
		r.rows++
	}
	if count == 0 {
		return nil, nil
	}

	return builder.NewRecordBatch(), nil
}

// appendObjectFields serializes an RDF object into the Iceberg object union columns.
func appendObjectFields(builder *array.RecordBuilder, t triple) error {
	objectIRI := builder.Field(2).(*array.StringBuilder)
	objectString := builder.Field(3).(*array.StringBuilder)
	objectGeometry := builder.Field(4).(*geoarrow.WKBBuilder)

	if t.oKind == objectKindIRI {
		objectIRI.Append(t.o)
		objectString.AppendNull()
		objectGeometry.AppendNull()
		return nil
	}

	if isWKTObject(t) {
		wkbBytes, err := wktObjectToWKB(t.o)
		if err != nil {
			return err
		}
		objectIRI.AppendNull()
		objectString.AppendNull()
		objectGeometry.Append(geoarrow.WKBBytes(wkbBytes))
		return nil
	}

	objectIRI.AppendNull()
	objectString.Append(t.o)
	objectGeometry.AppendNull()
	return nil
}

func isWKTObject(t triple) bool {
	return t.oKind == objectKindLiteral && t.oDatatype == geoSPARQLWKTLiteral
}

// wktObjectToWKB converts a GeoSPARQL WKT literal value into WKB bytes.
func wktObjectToWKB(value string) ([]byte, error) {
	geom, err := wkt.Unmarshal(stripGeoSPARQLCRS(value))
	if err != nil {
		return nil, fmt.Errorf("parse WKT %q: %w", value, err)
	}
	wkbBytes, err := wkb.Marshal(geom, binary.LittleEndian)
	if err != nil {
		return nil, fmt.Errorf("marshal WKB: %w", err)
	}
	return wkbBytes, nil
}

func stripGeoSPARQLCRS(value string) string {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "<") {
		return value
	}
	end := strings.Index(value, ">")
	if end == -1 {
		return value
	}
	return strings.TrimSpace(value[end+1:])
}

func (r *nquadRecordReader) nextTriple() (triple, bool, error) {
	for {
		if r.br == nil {
			if err := r.openNextFile(); err != nil {
				return triple{}, false, err
			}
			if r.br == nil {
				return triple{}, false, nil
			}
		}

		raw, err := r.br.ReadString('\n')
		if len(raw) > 0 {
			r.lineNum++
			line := cleanNQuadLine(raw)
			if line != "" {
				t, parseErr := parseNQuadLine(line)
				if parseErr != nil {
					log.Printf("  skipping %s line %d: %v", r.paths[r.fileIndex-1], r.lineNum, parseErr)
				} else {
					return t, true, nil
				}
			}
		}

		if err == nil {
			continue
		}
		if err == io.EOF {
			r.closeCurrentFile()
			continue
		}
		return triple{}, false, fmt.Errorf("%s line %d: %w", r.paths[r.fileIndex-1], r.lineNum, err)
	}
}

func (r *nquadRecordReader) openNextFile() error {
	if r.fileIndex >= len(r.paths) {
		return nil
	}

	path := r.paths[r.fileIndex]
	r.fileIndex++
	r.lineNum = 0
	log.Printf("Processing: %s", path)

	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}

	gz, err := gzip.NewReader(f)
	if err != nil {
		f.Close()
		return fmt.Errorf("gzip %s: %w", path, err)
	}

	r.file = f
	r.gz = gz
	r.br = bufio.NewReader(gz)
	return nil
}

func (r *nquadRecordReader) closeCurrentFile() {
	if r.gz != nil {
		r.gz.Close()
		r.gz = nil
	}
	if r.file != nil {
		r.file.Close()
		r.file = nil
	}
	r.br = nil
}

func (r *nquadRecordReader) releaseCurrent() {
	if r.current != nil {
		r.current.Release()
		r.current = nil
	}
}
