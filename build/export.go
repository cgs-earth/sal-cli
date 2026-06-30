package build

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/cgs-earth/sal/load"
	"github.com/cgs-earth/sal/pkg"
	rdflibgo "github.com/tggo/goRDFlib"
	"github.com/tggo/goRDFlib/nq"
)

type GraphExportFormat string

const (
	GraphExportFormatNQuads  GraphExportFormat = "nq"
	GraphExportFormatIceberg GraphExportFormat = "iceberg"
)

// Export graph takes in a rdflib format graph struct and
// serializes it to disk in the specified format
func ExportGraph(graph *rdflibgo.Graph, format GraphExportFormat) error {

	switch format {
	case "nq":
		dataDir, err := pkg.SalDataDir()
		if err != nil {
			return err
		}
		gitProject, err := pkg.GitProjectName()
		if err != nil {
			return err
		}
		fullOutPath := filepath.Join(dataDir, fmt.Sprintf("%s.nq", gitProject))
		file, err := os.Create(fullOutPath)
		if err != nil {
			return err
		}
		defer func() { _ = file.Close() }()

		err = nq.Serialize(graph, file)
		if err == nil {
			slog.Info("Saved built RDF data to " + fullOutPath)
		}
	case "iceberg":
		dataDir, err := pkg.SalDataDir()
		if err != nil {
			return err
		}
		gitProject, err := pkg.GitProjectName()
		if err != nil {
			return err
		}
		err = load.WriteGraphToIceberg(context.Background(), graph, &load.LoadCmd{
			BatchSize:          131072,
			ParquetCompression: "snappy",
			MetricsMode:        "none",
			Warehouse:          dataDir,
			Namespace:          gitProject,
		})
		if err != nil {
			return err
		}
		slog.Info("Saved built RDF data to Iceberg", "warehouse", dataDir, "namespace", gitProject)
	default:
		return fmt.Errorf("unknown output format: '%s'. Must be iceberg or nq", format)
	}

	return nil
}
