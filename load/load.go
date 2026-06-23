package load

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"path/filepath"
	"sync"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/iceberg-go"
	"github.com/apache/iceberg-go/catalog"
	"github.com/apache/iceberg-go/catalog/hadoop"
	"github.com/apache/iceberg-go/table"
)

type LoadCmd struct {
	BatchSize           int    `arg:"--batch-size" help:"Arrow records per batch" default:"131072"`
	Workers             int    `arg:"--workers" help:"number of input files to convert to Parquet in parallel" default:"8"`
	ParquetCompression  string `arg:"--compression" help:"Parquet compression codec: snappy, zstd, gzip, brotli, lz4, uncompressed" default:"snappy"`
	MetricsMode         string `arg:"--metrics-mode" help:"Iceberg metrics mode: none, counts, truncate(N), full" default:"truncate(16)"`
	TargetFileSizeBytes int64  `arg:"--target-file-size-bytes" help:"target Iceberg data file size"`
	InputDir            string `arg:"positional,required" placeholder:"PATH" help:"path to a directory containing .nq.gz files"`
	MaxFiles            int    `arg:"--max-files" help:"maximum number of input files to process" default:"0"`
	Warehouse           string `arg:"--warehouse" help:"Iceberg warehouse directory" default:"/tmp/iceberg-warehouse"`
	Namespace           string `arg:"--namespace" help:"Iceberg namespace" default:"default"`
}

func Run(cfg *LoadCmd) error {

	ctx := context.Background()

	arrowSchema, icebergSchema, err := GetSchemas()
	if err != nil {
		return err
	}

	cat, err := hadoop.NewCatalog("local-catalog", cfg.Warehouse, nil)
	if err != nil {
		log.Fatal("Failed to create catalog:", err)
	}

	tbl, err := NewIcebergTableFromCfg(ctx, icebergSchema, cat, cfg)
	if err != nil {
		slog.Error("Failed to create Iceberg table", "err", err)
		return err
	}

	pattern := filepath.Join(cfg.InputDir, "*.nq.gz")
	files, err := filepath.Glob(pattern)
	if err != nil {
		log.Fatal("Glob error:", err)
	}
	if len(files) == 0 {
		slog.Error("No .nq.gz files found", "input_dir", cfg.InputDir)
		return err
	}
	if cfg.MaxFiles > 0 && len(files) > cfg.MaxFiles {
		files = files[:cfg.MaxFiles]
	}
	log.Printf("Found %d .nq.gz file(s)", len(files))

	if err := applyWriteProperties(ctx, tbl, cfg); err != nil {
		log.Fatal("Failed to apply write properties:", err)
	}

	log.Printf("Write settings: workers=%d batch-size=%d compression=%s metrics=%s target-file-size=%d",
		cfg.Workers, cfg.BatchSize, cfg.ParquetCompression, cfg.MetricsMode, cfg.TargetFileSizeBytes)

	if err := processFiles(ctx, files, cat, tbl.Identifier(), arrowSchema, cfg.BatchSize, cfg.Workers); err != nil {
		log.Fatalf("Error processing files: %v", err)
	}

	log.Println("All files loaded successfully.")
	log.Println("Table location:", tbl.Location())
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
	dataFiles, err = assignFirstRowIDs(tbl.Spec(), dataFiles, tbl.Metadata().NextRowID())
	if err != nil {
		return err
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

// assignFirstRowIDs gives v3 Iceberg appends a stable row-id range for each data file.
func assignFirstRowIDs(spec *iceberg.PartitionSpec, dataFiles []iceberg.DataFile, nextRowID int64) ([]iceberg.DataFile, error) {
	assigned := make([]iceberg.DataFile, 0, len(dataFiles))
	for _, dataFile := range dataFiles {
		if dataFile.FirstRowID() != nil {
			assigned = append(assigned, dataFile)
			nextRowID = *dataFile.FirstRowID() + dataFile.Count()
			continue
		}

		rebuilt, err := rebuildDataFileWithFirstRowID(spec, dataFile, nextRowID)
		if err != nil {
			return nil, err
		}
		assigned = append(assigned, rebuilt)
		nextRowID += dataFile.Count()
	}
	return assigned, nil
}

func rebuildDataFileWithFirstRowID(spec *iceberg.PartitionSpec, dataFile iceberg.DataFile, firstRowID int64) (iceberg.DataFile, error) {
	builder, err := iceberg.NewDataFileBuilder(
		*spec,
		dataFile.ContentType(),
		dataFile.FilePath(),
		dataFile.FileFormat(),
		dataFile.Partition(),
		nil,
		nil,
		dataFile.Count(),
		dataFile.FileSizeBytes(),
	)
	if err != nil {
		return nil, fmt.Errorf("rebuild data file %s: %w", dataFile.FilePath(), err)
	}

	builder.FirstRowID(firstRowID)
	if sizes := dataFile.ColumnSizes(); sizes != nil {
		builder.ColumnSizes(sizes)
	}
	if counts := dataFile.ValueCounts(); counts != nil {
		builder.ValueCounts(counts)
	}
	if counts := dataFile.NullValueCounts(); counts != nil {
		builder.NullValueCounts(counts)
	}
	if counts := dataFile.NaNValueCounts(); counts != nil {
		builder.NaNValueCounts(counts)
	}
	if counts := dataFile.DistinctValueCounts(); counts != nil {
		builder.DistinctValueCounts(counts)
	}
	if bounds := dataFile.LowerBoundValues(); bounds != nil {
		builder.LowerBoundValues(bounds)
	}
	if bounds := dataFile.UpperBoundValues(); bounds != nil {
		builder.UpperBoundValues(bounds)
	}
	if key := dataFile.KeyMetadata(); key != nil {
		builder.KeyMetadata(key)
	}
	if offsets := dataFile.SplitOffsets(); offsets != nil {
		builder.SplitOffsets(offsets)
	}
	if ids := dataFile.EqualityFieldIDs(); ids != nil {
		builder.EqualityFieldIDs(ids)
	}
	if id := dataFile.SortOrderID(); id != nil {
		builder.SortOrderID(*id)
	}
	if referenced := dataFile.ReferencedDataFile(); referenced != nil {
		builder.ReferencedDataFile(*referenced)
	}
	if offset := dataFile.ContentOffset(); offset != nil {
		builder.ContentOffset(*offset)
	}
	if size := dataFile.ContentSizeInBytes(); size != nil {
		builder.ContentSizeInBytes(*size)
	}

	return builder.Build(), nil
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
