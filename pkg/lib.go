package pkg

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var ErrSalDirNotFound = errors.New("sal directory not found")
var ErrCantMakeSalDirInHome = errors.New("a sal project directory should not be the home directory; ~/.sal is intended for user-wide configuration")

// DefaultGitRemote returns the default git remote repository URL.
// It prefers origin, then falls back to the first configured remote.
func DefaultGitRemote() (string, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return "", fmt.Errorf("git is not installed or not on PATH: %w", err)
	}

	out, err := exec.Command("git", "remote", "get-url", "origin").Output()
	if err == nil {
		remote := strings.TrimSpace(string(out))
		if remote != "" {
			return remote, nil
		}
	}

	out, err = exec.Command("git", "remote").Output()
	if err != nil {
		return "", fmt.Errorf("failed to list git remotes: %w", err)
	}

	remotes := strings.Fields(string(out))
	if len(remotes) == 0 {
		return "", fmt.Errorf("git repository has no remotes configured")
	}

	out, err = exec.Command("git", "remote", "get-url", remotes[0]).Output()
	if err != nil {
		return "", fmt.Errorf("failed to get git remote %q URL: %w", remotes[0], err)
	}

	remoteURL := strings.TrimSpace(string(out))
	if remoteURL == "" {
		return "", fmt.Errorf("git remote %q has empty URL", remotes[0])
	}

	return remoteURL, nil
}

func DefaultSalBase() (string, error) {
	remote, err := DefaultGitRemote()
	if err != nil {
		return "", err
	}
	remote = strings.TrimSuffix(remote, ".git")
	if !strings.HasSuffix(remote, "/") {
		remote += "/"
	}
	return remote, nil
}

func GitProjectName() (string, error) {
	remote, err := DefaultGitRemote()
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(filepath.Base(remote), ".git"), nil
}

// SALProjectDir walks up from the current directory to find the nearest
// project-local .sal directory without crossing the user's home directory.
func SALProjectDir(getHomeDir func() (string, error)) (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to get current directory: %w", err)
	}

	home, err := getHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to determine user home directory: %w", err)
	}
	cwd, err = canonicalPath(cwd)
	if err != nil {
		return "", fmt.Errorf("failed to resolve current directory: %w", err)
	}
	home, err = canonicalPath(home)
	if err != nil {
		return "", fmt.Errorf("failed to resolve home directory: %w", err)
	}

	for {
		if cwd == home {
			return "", ErrCantMakeSalDirInHome
		}

		salDir := filepath.Join(cwd, ".sal")
		if info, err := os.Stat(salDir); err == nil && info.IsDir() {
			return cwd, nil
		}

		parent := filepath.Dir(cwd)
		if parent == cwd {
			break
		}

		cwd = parent
	}

	return "", ErrSalDirNotFound
}

func SalDataDir() (string, error) {
	salDir, err := SALProjectDir(os.UserHomeDir)
	if err != nil {
		return "", err
	}
	return filepath.Join(salDir, ".sal", "data"), nil
}

func canonicalPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}
