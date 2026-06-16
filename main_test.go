package main

import (
	"strings"
	"testing"

	"github.com/apache/iceberg-go/table"
)

func TestParseArgs(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		got := parseArgs([]string{"/data/nquads"})

		if !got.reset {
			t.Fatal("parseArgs().reset = false, want true")
		}
		if got.inputDir != "/data/nquads" {
			t.Fatalf("parseArgs().inputDir = %q, want %q", got.inputDir, "/data/nquads")
		}
		if got.batchSize != defaultBatchSize {
			t.Fatalf("parseArgs().batchSize = %d, want %d", got.batchSize, defaultBatchSize)
		}
		if got.parquetCompression != "snappy" {
			t.Fatalf("parseArgs().parquetCompression = %q, want snappy", got.parquetCompression)
		}
		if got.metricsMode != "truncate(16)" {
			t.Fatalf("parseArgs().metricsMode = %q, want truncate(16)", got.metricsMode)
		}
		if got.targetFileSizeBytes != table.WriteTargetFileSizeBytesDefault {
			t.Fatalf("parseArgs().targetFileSizeBytes = %d, want %d", got.targetFileSizeBytes, table.WriteTargetFileSizeBytesDefault)
		}
	})

	t.Run("overrides", func(t *testing.T) {
		got := parseArgs([]string{
			"--reset=false",
			"--workers=3",
			"--batch-size=1024",
			"--compression=zstd",
			"--metrics-mode=counts",
			"--target-file-size-bytes=2048",
			"/data/nquads",
		})

		if got.reset {
			t.Fatal("parseArgs().reset = true, want false")
		}
		if got.workers != 3 {
			t.Fatalf("parseArgs().workers = %d, want 3", got.workers)
		}
		if got.batchSize != 1024 {
			t.Fatalf("parseArgs().batchSize = %d, want 1024", got.batchSize)
		}
		if got.parquetCompression != "zstd" {
			t.Fatalf("parseArgs().parquetCompression = %q, want zstd", got.parquetCompression)
		}
		if got.metricsMode != "counts" {
			t.Fatalf("parseArgs().metricsMode = %q, want counts", got.metricsMode)
		}
		if got.targetFileSizeBytes != 2048 {
			t.Fatalf("parseArgs().targetFileSizeBytes = %d, want 2048", got.targetFileSizeBytes)
		}
	})
}

func TestParseNQuadLine(t *testing.T) {
	tests := []struct {
		name string
		line string
		want triple
	}{
		{
			name: "iri object",
			line: `<http://example.com/s> <http://example.com/p> <http://example.com/o> <http://example.com/g> .`,
			want: triple{s: "http://example.com/s", p: "http://example.com/p", o: "http://example.com/o"},
		},
		{
			name: "blank subject literal object",
			line: `_:b1 <http://example.com/p> "hello\nworld"@en . # trailing comment`,
			want: triple{s: "b1", p: "http://example.com/p", o: "hello\nworld"},
		},
		{
			name: "typed literal",
			line: `<http://example.com/s> <http://example.com/p> "42"^^<http://www.w3.org/2001/XMLSchema#integer> .`,
			want: triple{s: "http://example.com/s", p: "http://example.com/p", o: "42"},
		},
		{
			name: "escaped iri",
			line: `<http://example.com/\u0073> <http://example.com/p> _:obj .`,
			want: triple{s: "http://example.com/s", p: "http://example.com/p", o: "obj"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseNQuadLine(tt.line)
			if err != nil {
				t.Fatalf("parseNQuadLine() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("parseNQuadLine() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestParseNQuadsSkipsBadLines(t *testing.T) {
	input := strings.Join([]string{
		`<http://example.com/s1> <http://example.com/p> "ok" .`,
		`not valid`,
		`<http://example.com/s2> <http://example.com/p> <http://example.com/o> <http://example.com/g> .`,
	}, "\n")

	var got []triple
	if err := parseNQuads(strings.NewReader(input), func(t triple) error {
		got = append(got, t)
		return nil
	}); err != nil {
		t.Fatalf("parseNQuads() error = %v", err)
	}

	want := []triple{
		{s: "http://example.com/s1", p: "http://example.com/p", o: "ok"},
		{s: "http://example.com/s2", p: "http://example.com/p", o: "http://example.com/o"},
	}
	if len(got) != len(want) {
		t.Fatalf("parseNQuads() parsed %d triples, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("parseNQuads()[%d] = %#v, want %#v", i, got[i], want[i])
		}
	}
}
