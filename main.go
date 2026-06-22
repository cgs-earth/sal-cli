package main

import (
	"log/slog"
	"os"
	"time"

	"github.com/cgs-earth/sal-cli/build"
	"github.com/cgs-earth/sal-cli/load"

	"github.com/alexflint/go-arg"
	"github.com/lmittmann/tint"
)

type args struct {
	Load  *load.LoadCmd   `arg:"subcommand:load" help:"Load N-Quads gzip files into a local Iceberg triples table."`
	Build *build.BuildCmd `arg:"subcommand:build" help:"Build a vocabulary."`
}

func (args) Description() string {
	return "Validate and process RDF data"
}

func main() {
	slog.SetDefault(slog.New(
		tint.NewHandler(os.Stderr, &tint.Options{
			Level:      slog.LevelDebug,
			TimeFormat: time.Kitchen,
		}),
	))

	if len(os.Args) <= 1 {
		parseArgs()
		return
	}

	switch os.Args[1] {
	case "build":
		build.Run(os.Args[2:], os.Stdout, os.Stderr)
	case "load":
		args := parseArgs()
		load.RunLoadCommand(args.Load)
	default:
		parseArgs()
	}
}

func parseArgs() args {
	cli := args{}
	parser, err := arg.NewParser(arg.Config{}, &cli)
	if err != nil {
		slog.Error("Unable to create parser", "error", err)
		os.Exit(1)
	}
	err = parser.Parse(os.Args[1:])
	if err == arg.ErrHelp || len(os.Args) == 1 {
		parser.WriteHelp(os.Stdout)
		os.Exit(0)
	}
	if err != nil {
		slog.Error("Unable to parse command line arguments", "error", err)
		os.Exit(1)
	}

	return cli
}
