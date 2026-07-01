package initialization

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	_ "embed"

	"github.com/cgs-earth/sal/pkg"
)

//go:embed sal_config_example.jsonld
var salConfigTemplate string

type InitCmd struct {
}

func Run(cmd *InitCmd) error {

	// if cwd is home return an error
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	if cwd == home {
		return pkg.ErrCantMakeSalDirInHome
	}

	gitCmd := exec.Command("git", "remote", "-v")
	out, err := gitCmd.CombinedOutput()
	if err != nil {
		var exitErr *exec.ExitError

		switch {
		case errors.Is(err, exec.ErrNotFound):
			return fmt.Errorf("git is not installed or not in PATH; you must have git installed for SAL")

		case errors.As(err, &exitErr):
			log := string(out)
			if strings.Contains(log, "not a git repository") {
				return fmt.Errorf("current directory is not a git repository; SAL must be ran inside a git repository")
			}

			return fmt.Errorf("git command failed: %s", strings.TrimSpace(log))

		default:
			return fmt.Errorf("failed to execute git: %w", err)
		}
	}

	if strings.TrimSpace(string(out)) == "" {
		return fmt.Errorf("git repository has no remotes configured; you must specify a remote before running init")
	}

	cwd, err = os.Getwd()
	if err != nil {
		return err
	}
	salDataDir := filepath.Join(cwd, ".sal", "data")

	err = os.MkdirAll(salDataDir, 0755)
	if err != nil {
		return err
	}

	home, err = os.UserHomeDir()
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

	// check if .gitignore is present in the cwd, if not create it and add .sal/data to it
	gitignorePath := filepath.Join(cwd, ".gitignore")
	if _, err := os.Stat(gitignorePath); os.IsNotExist(err) {
		err = os.WriteFile(gitignorePath, []byte(".sal/data\n"), 0644)
		if err != nil {
			return err
		}
	} else if err == nil {
		// check if .sal/data is already in .gitignore
		content, err := os.ReadFile(gitignorePath)
		if err != nil {
			return err
		}
		if !strings.Contains(string(content), ".sal/data") {
			f, err := os.OpenFile(gitignorePath, os.O_APPEND|os.O_WRONLY, 0644)
			if err != nil {
				return err
			}
			defer func() { _ = f.Close() }()
			if _, err := f.WriteString("\n.sal/data\n"); err != nil {
				return err
			}
		}
	}

	slog.Info("SAL project initialized at " + cwd)
	return nil
}
