package load

import (
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"

	"github.com/apache/arrow-go/v18/arrow/array"
	"github.com/apache/iceberg-go/table"
	geoarrow "github.com/geoarrow/geoarrow-go"
	"github.com/stretchr/testify/require"
)

func TestGetSchemasUseCompatibleGeometryTypes(t *testing.T) {
	arrowSchema, icebergSchema, err := GetSchemas()
	require.NoError(t, err)

	convertedType, err := table.ArrowTypeToIceberg(arrowSchema.Field(4).Type, false)
	require.NoError(t, err)
	require.Equal(t, icebergSchema.Field(4).Type.String(), convertedType.String())
}

func TestNQuadRecordReaderStreamsFiles(t *testing.T) {
	dir := t.TempDir()
	first := writeGzipNQuads(t, dir, "first.nq.gz", []string{
		`<http://example.com/s1> <http://example.com/p> "one" .`,
		`not valid`,
	})
	second := writeGzipNQuads(t, dir, "second.nq.gz", []string{
		`<http://example.com/s2> <http://example.com/p> "two" .`,
		`<http://example.com/s3> <http://example.com/p> "three" .`,
	})

	arrowSchema, _, err := GetSchemas()
	require.NoError(t, err)
	rdr := newNQuadRecordReader([]string{first, second}, arrowSchema, 2)
	defer rdr.Release()

	var batches int
	var rows int64
	for rdr.Next() {
		batches++
		rows += rdr.RecordBatch().NumRows()
	}
	require.NoError(t, rdr.Err())
	require.Equal(t, 2, batches)
	require.Equal(t, int64(3), rows)
	require.Equal(t, int64(3), rdr.RowsRead())
}

func TestNQuadRecordReaderSerializesObjectColumns(t *testing.T) {
	dir := t.TempDir()
	path := writeGzipNQuads(t, dir, "objects.nq.gz", []string{
		`<http://example.com/s1> <http://example.com/p> <http://example.com/o> .`,
		`<http://example.com/s2> <http://example.com/p> "label" .`,
		`<http://example.com/s3> <http://example.com/p> "POINT (1 2)"^^<http://www.opengis.net/ont/geosparql#wktLiteral> .`,
	})

	arrowSchema, _, err := GetSchemas()
	require.NoError(t, err)
	rdr := newNQuadRecordReader([]string{path}, arrowSchema, 10)
	defer rdr.Release()

	require.True(t, rdr.Next())
	rec := rdr.RecordBatch()
	require.Equal(t, int64(3), rec.NumRows())

	objectIRI := rec.Column(2).(*array.String)
	objectString := rec.Column(3).(*array.String)
	objectGeometry := rec.Column(4).(*geoarrow.WKBArray)

	require.Equal(t, "http://example.com/o", objectIRI.Value(0))
	require.True(t, objectString.IsNull(0))
	require.True(t, objectGeometry.IsNull(0))

	require.True(t, objectIRI.IsNull(1))
	require.Equal(t, "label", objectString.Value(1))
	require.True(t, objectGeometry.IsNull(1))

	expectedWKB, err := wktObjectToWKB("POINT (1 2)")
	require.NoError(t, err)
	require.True(t, objectIRI.IsNull(2))
	require.True(t, objectString.IsNull(2))
	require.Equal(t, geoarrow.WKBBytes(expectedWKB), objectGeometry.Value(2))

	require.False(t, rdr.Next())
	require.NoError(t, rdr.Err())
}

func writeGzipNQuads(t *testing.T, dir, name string, lines []string) string {
	t.Helper()

	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	require.NoError(t, err)
	gz := gzip.NewWriter(f)
	for _, line := range lines {
		_, err := gz.Write([]byte(line + "\n"))
		require.NoError(t, err)
	}
	require.NoError(t, gz.Close())
	require.NoError(t, f.Close())
	return path
}
