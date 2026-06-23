package load

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"os"
	"strconv"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/iceberg-go"
	"github.com/apache/iceberg-go/catalog"
	"github.com/apache/iceberg-go/table"
	geoarrow "github.com/geoarrow/geoarrow-go"
)

func GetSchemas() (*arrow.Schema, *iceberg.Schema, error) {
	geoCRS, err := json.Marshal("OGC:CRS84")
	if err != nil {
		return nil, nil, err
	}
	geoMetadata := geoarrow.Metadata{
		CRS:     geoCRS,
		CRSType: geoarrow.CRSTypeAuthorityCode,
	}

	var arrowSchema = arrow.NewSchema(
		[]arrow.Field{
			{Name: "subject", Type: arrow.BinaryTypes.String},
			{Name: "predicate", Type: arrow.BinaryTypes.String},
			{Name: "object_iri", Type: arrow.BinaryTypes.String, Nullable: true},
			{Name: "object_string", Type: arrow.BinaryTypes.String, Nullable: true},
			{Name: "object_geometry", Type: geoarrow.NewWKBType(geoarrow.WKBWithBinaryStorage(), geoarrow.WKBWithMetadata(geoMetadata)), Nullable: true},
		},
		nil,
	)

	geometry_type, err := iceberg.GeometryTypeOf("OGC:CRS84")
	if err != nil {
		return nil, nil, err
	}
	var icebergSchema = iceberg.NewSchemaWithIdentifiers(1, []int{3},
		iceberg.NestedField{ID: 1, Name: "subject", Type: iceberg.PrimitiveTypes.String, Required: true},
		iceberg.NestedField{ID: 2, Name: "predicate", Type: iceberg.PrimitiveTypes.String, Required: true},
		iceberg.NestedField{ID: 3, Name: "object_iri", Type: iceberg.PrimitiveTypes.String, Required: false},
		iceberg.NestedField{ID: 4, Name: "object_string", Type: iceberg.PrimitiveTypes.String, Required: false},
		iceberg.NestedField{ID: 5, Name: "object_geometry", Type: geometry_type, Required: false},
	)

	return arrowSchema, icebergSchema, nil

}

func NewIcebergTableFromCfg(ctx context.Context, tableSchema *iceberg.Schema, cat catalog.Catalog, cfg *LoadCmd) (*table.Table, error) {

	if err := os.MkdirAll(cfg.Warehouse+"/"+cfg.Namespace, 0755); err != nil {
		slog.Error("Failed to create warehouse directory:", "error", err)
		return nil, err
	}

	defaultNS := catalog.ToIdentifier(cfg.Namespace)
	if err := cat.CreateNamespace(ctx, defaultNS, nil); err != nil &&
		!errors.Is(err, catalog.ErrNamespaceAlreadyExists) {
		slog.Error("Failed to create default namespace:", "error", err)
		return nil, err
	}

	tableIdent := catalog.ToIdentifier(cfg.Namespace, "triples")
	if err := cat.DropTable(ctx, tableIdent); err != nil && !errors.Is(err, catalog.ErrNoSuchTable) {
		log.Fatal("Failed to reset table:", err)
	}
	slog.Info("Table reset successfully")

	partitionSpec := iceberg.NewPartitionSpec(
		iceberg.PartitionField{
			SourceIDs: []int{2},
			Transform: iceberg.TruncateTransform{Width: 20},
			Name:      "predicate_partition",
		},
	)

	sortField := table.SortField{
		SourceIDs: []int{2},
		Transform: iceberg.IdentityTransform{},
		Direction: table.SortASC,
		NullOrder: table.NullsLast,
	}
	sortOrder, err := table.NewSortOrder(table.InitialSortOrderID, []table.SortField{sortField})
	if err != nil {
		return nil, err
	}

	return cat.CreateTable(ctx, tableIdent, tableSchema,
		catalog.WithPartitionSpec(&partitionSpec),
		catalog.WithSortOrder(sortOrder),
		catalog.WithProperties(map[string]string{
			table.MetadataDeleteAfterCommitEnabledKey:              "true",
			table.MetadataPreviousVersionsMaxKey:                   strconv.Itoa(1),
			table.ManifestMergeEnabledKey:                          "true",
			table.ManifestMinMergeCountKey:                         strconv.Itoa(1),
			"write.parquet.compression-codec":                      cfg.ParquetCompression,
			"write.metadata.metrics.default":                       cfg.MetricsMode,
			table.MetricsModeColumnConfPrefix + ".object_geometry": "none",
			table.WriteTargetFileSizeBytesKey:                      strconv.FormatInt(cfg.TargetFileSizeBytes, 10),
			table.WriteDeleteModeKey:                               table.WriteModeMergeOnRead,
			// override to version 3 so we can use geometry while it is in development
			table.PropertyFormatVersion: "3",
		}),
	)
}

func applyWriteProperties(ctx context.Context, tbl *table.Table, cfg *LoadCmd) error {
	writeProps := iceberg.Properties{
		"write.parquet.compression-codec":                      cfg.ParquetCompression,
		"write.metadata.metrics.default":                       cfg.MetricsMode,
		table.MetricsModeColumnConfPrefix + ".object_geometry": "none",
		table.WriteTargetFileSizeBytesKey:                      strconv.FormatInt(cfg.TargetFileSizeBytes, 10),
	}

	txn := tbl.NewTransaction()
	if err := txn.SetProperties(writeProps); err != nil {
		return fmt.Errorf("set table properties: %w", err)
	}
	if _, err := txn.Commit(ctx); err != nil {
		return fmt.Errorf("commit table properties: %w", err)
	}
	return nil
}
