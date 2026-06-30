package main

import (
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/cgs-earth/sal/build"
	"github.com/cgs-earth/sal/clean"
	"github.com/cgs-earth/sal/initialization"
	"github.com/cgs-earth/sal/load"
	"github.com/cgs-earth/sal/query"

	"github.com/alexflint/go-arg"
	"github.com/lmittmann/tint"
)

// All subcommands that sal supports. These should be in a useful order as
// the order changes how the CLI presents them in the help message.
type args struct {
	Init  *initialization.InitCmd `arg:"subcommand:init" help:"Initialize a SAL project."`
	Load  *load.LoadCmd           `arg:"subcommand:load" help:"Load N-Quads gzip files into a local Iceberg triples table."`
	Build *build.BuildCmd         `arg:"subcommand:build" help:"Build a vocabulary."`
	Query *query.QueryCmd         `arg:"subcommand:query" help:"Use duckdb to query a built SAL data product."`
	Clean *clean.CleanCmd         `arg:"subcommand:clean" help:"Clean build artifacts produced by a SAL project."`
}

func (args) Description() string {
	return "Validate and process RDF data"
}

func main() {

	slog.SetDefault(slog.New(
		tint.NewHandler(os.Stderr, &tint.Options{
			Level:      slog.LevelDebug,
			TimeFormat: time.Kitchen,
			AddSource:  true,
		}),
	))

	if len(os.Args) == 1 {
		os.Args = append(os.Args, "--help")
	}

	var cli args
	arg.MustParse(&cli)
	var err error
	switch {
	case cli.Build != nil:
		_, err = build.Run(cli.Build)
		// Errors from build should be directly written to stdout
		// not written as a log which adds extra noise
		if err != nil {
			fmt.Println(err.Error())
			os.Exit(1)
		}
	case cli.Load != nil:
		err = load.Run(cli.Load)
	case cli.Init != nil:
		err = initialization.Run(cli.Init)
	case cli.Query != nil:
		err = query.Run(cli.Query)
	case cli.Clean != nil:
		err = clean.Run(cli.Clean)
	}
	if err != nil {
		slog.Error(err.Error())
		os.Exit(1)
	}
}
