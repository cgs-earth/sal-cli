package pkg

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFindSALProjectDirFindsNearestProjectDirBelowHome(t *testing.T) {
	home := testHome(t)
	project := filepath.Join(home, "work", "project")
	nested := filepath.Join(project, "data", "rdf")
	require.NoError(t, os.MkdirAll(filepath.Join(project, ".sal"), 0755))
	require.NoError(t, os.MkdirAll(nested, 0755))
	chdir(t, nested)

	got, err := SALProjectDir(func() (string, error) {
		return home, nil
	})

	require.NoError(t, err)
	want, err := canonicalPath(project)
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestFindSALProjectDirFailsForHomeSALDir(t *testing.T) {
	home := testHome(t)
	nested := filepath.Join(home, "work", "project")
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".sal"), 0755))
	require.NoError(t, os.MkdirAll(nested, 0755))
	chdir(t, nested)

	got, err := SALProjectDir(func() (string, error) {
		return home, nil
	})

	require.Empty(t, got)
	require.True(t, errors.Is(err, ErrCantMakeSalDirInHome), "error = %v", err)
}

func TestFindSALProjectDirDoesNotSearchAboveHome(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	nested := filepath.Join(home, "work", "project")
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".sal"), 0755))
	require.NoError(t, os.MkdirAll(nested, 0755))
	chdir(t, nested)

	got, err := SALProjectDir(func() (string, error) {
		return home, nil
	})

	require.Empty(t, got)
	require.True(t, errors.Is(err, ErrCantMakeSalDirInHome), "error = %v", err)
}

func testHome(t *testing.T) string {
	t.Helper()
	home := filepath.Join(t.TempDir(), "home")
	require.NoError(t, os.MkdirAll(home, 0755))
	return home
}

func chdir(t *testing.T, dir string) {
	t.Helper()
	previous, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() {
		require.NoError(t, os.Chdir(previous))
	})
}
