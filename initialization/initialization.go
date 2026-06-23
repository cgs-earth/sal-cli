package initialization

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	_ "embed"
)

//go:embed sal_config_example.jsonld
var salConfigTemplate string

type InitCmd struct {
}

func Run(cmd *InitCmd) error {

	gitCmd := exec.Command("git", "remote")
	out, err := gitCmd.Output()
	if err != nil {
		return fmt.Errorf("current directory is not a git repository: %w", err)
	}

	if strings.TrimSpace(string(out)) == "" {
		return fmt.Errorf("git repository has no remotes configured")
	}

	// Verify this git repository has at least one remote configured.
	out, err = exec.Command("git", "remote", "-v").Output()
	if err != nil {
		return fmt.Errorf("failed to check git remotes: %w", err)
	}

	if len(out) == 0 {
		return fmt.Errorf("no git remotes configured; add an associated git repository before running init")
	}

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	salDataDir := filepath.Join(cwd, ".sal", "data")

	err = os.MkdirAll(salDataDir, 0755)
	if err != nil {
		return err
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	salCacheDir := filepath.Join(home, ".sal", "cache")

	err = os.MkdirAll(salCacheDir, 0755)
	if err != nil {
		return err
	}

	err = os.WriteFile(filepath.Join(home, ".sal", "config.jsonld"), []byte(salConfigTemplate), 0644)
	if err != nil {
		return err
	}
	slog.Info("SAL project initialized at " + cwd)
	return nil
}
