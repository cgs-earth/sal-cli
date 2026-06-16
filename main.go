package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"

	"github.com/alexflint/go-arg"
	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/iceberg-go"
	"github.com/apache/iceberg-go/catalog"
	"github.com/apache/iceberg-go/catalog/hadoop"
	_ "github.com/apache/iceberg-go/catalog/hadoop"
	"github.com/apache/iceberg-go/table"
)

const defaultBatchSize = 131_072

type loadConfig struct {
	inputDir            string
	batchSize           int
	workers             int
	parquetCompression  string
	metricsMode         string
	targetFileSizeBytes int64
}

type cliArgs struct {
	BatchSize           int    `arg:"--batch-size" help:"Arrow records per batch"`
	Workers             int    `arg:"--workers" help:"number of input files to convert to Parquet in parallel"`
	ParquetCompression  string `arg:"--compression" help:"Parquet compression codec: snappy, zstd, gzip, brotli, lz4, uncompressed"`
	MetricsMode         string `arg:"--metrics-mode" help:"Iceberg metrics mode: none, counts, truncate(N), full"`
	TargetFileSizeBytes int64  `arg:"--target-file-size-bytes" help:"target Iceberg data file size"`
	InputDir            string `arg:"positional,required" placeholder:"PATH" help:"path to a directory containing .nq.gz files"`
}

func (cliArgs) Description() string {
	return "Load N-Quads gzip files into a local Iceberg triples table."
}

type triple struct{ s, p, o string }

func main() {
	cfg := parseArgs(os.Args[1:])

	ctx := context.Background()

	if err := os.MkdirAll("/tmp/iceberg-warehouse/default", 0755); err != nil {
		log.Fatal("Failed to create warehouse directory:", err)
	}

	cat, err := hadoop.NewCatalog("local-catalog", "/tmp/iceberg-warehouse", nil)
	if err != nil {
		log.Fatal("Failed to create catalog:", err)
	}

	defaultNS := catalog.ToIdentifier("default")
	if err := cat.CreateNamespace(ctx, defaultNS, nil); err != nil &&
		err.Error() != "namespace already exists: default" {
		log.Fatal("Failed to create default namespace:", err)
	}
	log.Println("Namespace ready")

	icebergSchema := iceberg.NewSchemaWithIdentifiers(1, []int{3},
		iceberg.NestedField{ID: 1, Name: "subject", Type: iceberg.PrimitiveTypes.String, Required: true},
		iceberg.NestedField{ID: 2, Name: "predicate", Type: iceberg.PrimitiveTypes.String, Required: true},
		iceberg.NestedField{ID: 3, Name: "object", Type: iceberg.PrimitiveTypes.String, Required: true},
	)

	tableIdent := catalog.ToIdentifier("default", "triples")
	if err := cat.DropTable(ctx, tableIdent); err != nil && !errors.Is(err, catalog.ErrNoSuchTable) {
		log.Fatal("Failed to reset table:", err)
	}
	log.Println("Reset table default.triples")

	partitionSpec := iceberg.NewPartitionSpec(
		iceberg.PartitionField{SourceIDs: []int{2}, Transform: iceberg.TruncateTransform{Width: 20}, Name: "predicate_partition"},
	)

	sortField := table.SortField{SourceIDs: []int{2}, Direction: table.SortASC, Transform: iceberg.IdentityTransform{}}
	sortOrder, err := table.NewSortOrder(table.InitialSortOrderID, []table.SortField{sortField})

	tbl, err := cat.CreateTable(ctx, tableIdent, icebergSchema,
		catalog.WithPartitionSpec(&partitionSpec),
		catalog.WithSortOrder(sortOrder),
		catalog.WithProperties(map[string]string{
			table.MetadataDeleteAfterCommitEnabledKey: "true",
			table.MetadataPreviousVersionsMaxKey:      strconv.Itoa(1),
			table.ManifestMergeEnabledKey:             "true",
			table.ManifestMinMergeCountKey:            strconv.Itoa(1),
			"write.parquet.compression-codec":         cfg.parquetCompression,
			"write.metadata.metrics.default":          cfg.metricsMode,
			table.WriteTargetFileSizeBytesKey:         strconv.FormatInt(cfg.targetFileSizeBytes, 10),
			table.WriteDeleteModeKey:                  table.WriteModeMergeOnRead,
		}),
	)
	createdTable := err == nil
	if err != nil {
		log.Print("Failed to create table, loading existing table:", err)
		tbl, err = cat.LoadTable(ctx, tableIdent)
		if err != nil {
			log.Fatalf("Failed to load existing table: %v.", err)
		}
	} else {
		log.Println("Table created successfully")
	}

	arrowSchema := arrow.NewSchema(
		[]arrow.Field{
			{Name: "subject", Type: arrow.BinaryTypes.String},
			{Name: "predicate", Type: arrow.BinaryTypes.String},
			{Name: "object", Type: arrow.BinaryTypes.String},
		},
		nil,
	)

	pattern := filepath.Join(cfg.inputDir, "*.nq.gz")
	files, err := filepath.Glob(pattern)
	if err != nil {
		log.Fatal("Glob error:", err)
	}
	if len(files) == 0 {
		log.Fatalf("No .nq.gz files found in %s", cfg.inputDir)
	}
	log.Printf("Found %d .nq.gz file(s)", len(files))

	writeProps := iceberg.Properties{
		"write.parquet.compression-codec": cfg.parquetCompression,
		"write.metadata.metrics.default":  cfg.metricsMode,
		table.WriteTargetFileSizeBytesKey: strconv.FormatInt(cfg.targetFileSizeBytes, 10),
	}
	if !createdTable {
		if err := applyWriteProperties(ctx, tbl, writeProps); err != nil {
			log.Fatalf("Failed to configure table write properties: %v", err)
		}
	}
	log.Printf("Write settings: workers=%d batch-size=%d compression=%s metrics=%s target-file-size=%d",
		cfg.workers, cfg.batchSize, cfg.parquetCompression, cfg.metricsMode, cfg.targetFileSizeBytes)

	if err := processFiles(ctx, files, cat, tableIdent, arrowSchema, cfg.batchSize, cfg.workers); err != nil {
		log.Fatalf("Error processing files: %v", err)
	}

	log.Println("All files loaded successfully.")
	log.Println("Table location:", tbl.Location())
}

func parseArgs(args []string) loadConfig {
	cli := cliArgs{
		BatchSize:           defaultBatchSize,
		Workers:             runtime.GOMAXPROCS(0),
		ParquetCompression:  "snappy",
		MetricsMode:         "truncate(16)",
		TargetFileSizeBytes: table.WriteTargetFileSizeBytesDefault,
	}
	parser, err := arg.NewParser(arg.Config{Program: "loader"}, &cli)
	if err != nil {
		log.Fatal(err)
	}
	parser.MustParse(args)

	if cli.BatchSize <= 0 {
		log.Fatal("--batch-size must be positive")
	}
	if cli.Workers <= 0 {
		log.Fatal("--workers must be positive")
	}
	if cli.TargetFileSizeBytes <= 0 {
		log.Fatal("--target-file-size-bytes must be positive")
	}
	return loadConfig{
		inputDir:            cli.InputDir,
		batchSize:           cli.BatchSize,
		workers:             cli.Workers,
		parquetCompression:  cli.ParquetCompression,
		metricsMode:         cli.MetricsMode,
		targetFileSizeBytes: cli.TargetFileSizeBytes,
	}
}

func applyWriteProperties(ctx context.Context, tbl *table.Table, props iceberg.Properties) error {
	txn := tbl.NewTransaction()
	if err := txn.SetProperties(props); err != nil {
		return fmt.Errorf("set table properties: %w", err)
	}
	if _, err := txn.Commit(ctx); err != nil {
		return fmt.Errorf("commit table properties: %w", err)
	}
	return nil
}

// processFiles writes each .nq.gz input to Iceberg data files in parallel, then
// commits all of the produced data files in one table snapshot.
func processFiles(
	ctx context.Context,
	files []string,
	cat catalog.Catalog,
	tableIdent table.Identifier,
	arrowSchema *arrow.Schema,
	batchSize int,
	workers int,
) error {
	tbl, err := cat.LoadTable(ctx, tableIdent)
	if err != nil {
		return fmt.Errorf("load table: %w", err)
	}

	dataFiles, rows, err := writeFilesInParallel(ctx, tbl, files, arrowSchema, batchSize, workers)
	if err != nil {
		return err
	}
	if len(dataFiles) == 0 {
		return fmt.Errorf("no triples found")
	}

	txn := tbl.NewTransaction()
	if err := txn.AddDataFiles(ctx, dataFiles, iceberg.Properties(nil), table.WithoutDuplicateCheck()); err != nil {
		return fmt.Errorf("stage data files: %w", err)
	}
	if _, err := txn.Commit(ctx); err != nil {
		return fmt.Errorf("commit data files: %w", err)
	}

	log.Printf("  committed %d parquet data file(s) with %d triples in one snapshot", len(dataFiles), rows)
	return nil
}

func writeFilesInParallel(
	ctx context.Context,
	tbl *table.Table,
	files []string,
	arrowSchema *arrow.Schema,
	batchSize int,
	workers int,
) ([]iceberg.DataFile, int64, error) {
	if workers > len(files) {
		workers = len(files)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	type result struct {
		dataFiles []iceberg.DataFile
		rows      int64
		err       error
	}

	jobs := make(chan string)
	results := make(chan result, len(files))
	var wg sync.WaitGroup

	for i := 0; i < workers; i++ {
		wg.Go(func() {
			for path := range jobs {
				dataFiles, rows, err := writeOneInputFile(ctx, tbl, path, arrowSchema, batchSize)
				if err != nil {
					cancel()
				}
				results <- result{dataFiles: dataFiles, rows: rows, err: err}
			}
		})
	}

	go func() {
		defer close(jobs)
		for _, path := range files {
			select {
			case jobs <- path:
			case <-ctx.Done():
				return
			}
		}
	}()

	go func() {
		wg.Wait()
		close(results)
	}()

	var allDataFiles []iceberg.DataFile
	var totalRows int64
	for res := range results {
		if res.err != nil {
			return nil, 0, res.err
		}
		allDataFiles = append(allDataFiles, res.dataFiles...)
		totalRows += res.rows
	}

	return allDataFiles, totalRows, nil
}

func writeOneInputFile(
	ctx context.Context,
	tbl *table.Table,
	path string,
	arrowSchema *arrow.Schema,
	batchSize int,
) ([]iceberg.DataFile, int64, error) {
	rdr := newNQuadRecordReader([]string{path}, arrowSchema, batchSize)
	defer rdr.Release()

	records := retainedRecordIterator(rdr)
	var dataFiles []iceberg.DataFile
	for df, err := range table.WriteRecords(ctx, tbl, arrowSchema, records) {
		if err != nil {
			return nil, 0, fmt.Errorf("write %s: %w", path, err)
		}
		dataFiles = append(dataFiles, df)
	}
	if err := rdr.Err(); err != nil {
		return nil, 0, fmt.Errorf("read %s: %w", path, err)
	}

	log.Printf("  wrote %s as %d parquet data file(s), %d triples", path, len(dataFiles), rdr.RowsRead())
	return dataFiles, rdr.RowsRead(), nil
}

func retainedRecordIterator(rdr *nquadRecordReader) func(func(arrow.RecordBatch, error) bool) {
	return func(yield func(arrow.RecordBatch, error) bool) {
		for rdr.Next() {
			rec := rdr.RecordBatch()
			rec.Retain()
			if !yield(rec, nil) {
				return
			}
		}
		if err := rdr.Err(); err != nil {
			yield(nil, err)
		}
	}
}
