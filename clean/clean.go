package clean

import (
	"log/slog"
	"os"
	"path/filepath"

	"github.com/cgs-earth/sal/pkg"
)

type CleanCmd struct {
}

func Run(cmd *CleanCmd) error {
	salDataDir, err := pkg.SalDataDir()
	if err != nil {
		return err
	}
	files, err := os.ReadDir(salDataDir)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		slog.Info("No data to clean in " + salDataDir)
		return nil
	}

	for _, file := range files {
		if err := os.RemoveAll(filepath.Join(salDataDir, file.Name())); err != nil {
			return err
		}
	}
	slog.Info("Removed all build artifacts in " + salDataDir)
	return nil
}
