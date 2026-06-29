package build

import (
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/cgs-earth/sal/build/validate"
	"github.com/cgs-earth/sal/pkg"
	rdflibgo "github.com/tggo/goRDFlib"
)

type BuildCmd struct {
	Paths      []string          `arg:"positional" help:"RDF files to validate"`
	PrefixMaps []string          `arg:"--prefix-maps" help:"prefix mappings to apply as source target pairs or source=target entries"`
	Format     GraphExportFormat `arg:"--format" help:"output format: nq or iceberg" default:"iceberg"`
}

var findSALProjectDir = pkg.SALProjectDir

// Run validates RDF files for terms that are not defined by their vocabularies and returns their merged RDF graph.
func Run(cfg *BuildCmd) (*rdflibgo.Graph, error) {
	if cfg == nil {
		return nil, fmt.Errorf("build: missing arguments")
	}

	var paths []string
	if len(cfg.Paths) > 0 {
		paths = cfg.Paths
	} else {
		projectDir, err := findSALProjectDir(os.UserHomeDir)
		if err != nil {
			return nil, fmt.Errorf("build: find SAL project directory: %w", err)
		}
		paths = []string{projectDir}
	}

	base, err := pkg.DefaultSalBase()
	if err != nil {
		return nil, err
	}
	files, err := pkg.FindRdfDataInPaths(paths)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no JSON-LD or TTL files found in %s", strings.Join(paths, ", "))
	}

	vocabsToReplace, err := parsePrefixMaps(cfg.PrefixMaps)
	if err != nil {
		return nil, err
	}

	finalGraph := rdflibgo.NewGraph(rdflibgo.WithBase(base))
	var errs validate.MultiError
	for _, file := range files {
		// TODO do this in parallel.
		graph, err := validate.ValidateRDFFile(file, vocabsToReplace, base)
		if err != nil {
			if nested, ok := err.(validate.MultiError); ok {
				errs = append(errs, nested...)
			} else {
				errs = append(errs, err)
			}
			continue
		}
		mergeGraph(finalGraph, graph)
	}
	if len(errs) > 0 {
		return nil, errs
	}

	slog.Info("Validated " + fmt.Sprint(len(files)) + " file(s)")

	if err := NewTermsHaveClassDefinitions(finalGraph); err != nil {
		return nil, err
	}

	if err := ExportGraph(finalGraph, cfg.Format); err != nil {
		return nil, err
	}

	return finalGraph, err
}

func parsePrefixMaps(values []string) (map[string]string, error) {
	mappings := map[string]string{}
	for i := 0; i < len(values); i++ {
		value := strings.TrimSpace(values[i])
		if value == "" {
			continue
		}
		if source, target, ok := strings.Cut(value, "="); ok {
			source = strings.TrimSpace(source)
			target = strings.TrimSpace(target)
			if source == "" || target == "" {
				return nil, fmt.Errorf("build: invalid prefix mapping %q", value)
			}
			mappings[source] = target
			continue
		}
		if i+1 >= len(values) {
			return nil, fmt.Errorf("build: prefix mapping %q missing target", value)
		}
		target := strings.TrimSpace(values[i+1])
		if target == "" {
			return nil, fmt.Errorf("build: prefix mapping %q missing target", value)
		}
		mappings[value] = target
		i++
	}
	return mappings, nil
}
