package main

import (
	"log/slog"
	"os"
	"time"

	"github.com/cgs-earth/sal-cli/build"
	"github.com/cgs-earth/sal-cli/load"

	"github.com/alexflint/go-arg"
	_ "github.com/apache/iceberg-go/catalog/hadoop"
	"github.com/lmittmann/tint"
)

type args struct {
	Load *load.LoadCmd `arg:"subcommand:load" help:"Load N-Quads gzip files into a local Iceberg triples table."`
}

func (args) Description() string {
	return "Load N-Quads gzip files into a local Iceberg triples table."
}

func main() {
	slog.SetDefault(slog.New(
		tint.NewHandler(os.Stderr, &tint.Options{
			Level:      slog.LevelDebug,
			TimeFormat: time.Kitchen,
		}),
	))

	if len(os.Args) > 1 {
		args := parseArgs()
		switch os.Args[1] {
		case "build":
			os.Exit(build.Run(os.Args[2:], os.Stdout, os.Stderr))
		case "load":
			load.RunLoadCommand(args.Load)
		}
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
	if err == arg.ErrHelp {
		parser.WriteHelp(os.Stdout)
		os.Exit(0)
	}
	if err != nil {
		slog.Error("Unable to parse command line arguments", "error", err)
		os.Exit(1)
	}

	return cli
}
