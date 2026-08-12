package intercept

import (
	"errors"
	"os"
	"runtime"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractInjector_currentPlatform(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	p, err := extractInjector(dir)
	require.NoError(t, err, "the placeholder for the test platform must be extractable")

	info, err := os.Stat(p)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), info.Mode().Perm(), "injector must be extracted at mode 0700")

	data, err := os.ReadFile(p)
	require.NoError(t, err)
	assert.NotEmpty(t, data, "extracted injector must not be empty")
}

func TestExtractInjectorFor_found(t *testing.T) {
	t.Parallel()

	fsys := fstest.MapFS{
		"injector/linux_amd64/lib.so": {Data: []byte("fake-lib")},
	}
	dir := t.TempDir()

	p, err := extractInjectorFor(fsys, "linux", "amd64", dir)
	require.NoError(t, err)

	info, err := os.Stat(p)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), info.Mode().Perm())

	data, err := os.ReadFile(p)
	require.NoError(t, err)
	assert.Equal(t, "fake-lib", string(data))
}

func TestExtractInjectorFor_unknownPlatform(t *testing.T) {
	t.Parallel()

	fsys := fstest.MapFS{
		"injector/linux_amd64/lib.so": {Data: []byte("fake-lib")},
	}
	dir := t.TempDir()

	_, err := extractInjectorFor(fsys, "plan9", "mips64", dir)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrNoInjector), "unknown platform must yield ErrNoInjector")
}

func TestExtractInjectorFor_emptyPlatformDir(t *testing.T) {
	t.Parallel()

	// A subdirectory that exists but has only nested dirs (no regular file).
	fsys := fstest.MapFS{
		"injector/linux_amd64/nested/lib.so": {Data: []byte("fake-lib")},
	}
	dir := t.TempDir()

	_, err := extractInjectorFor(fsys, "linux", "amd64", dir)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrNoInjector))
}

func TestInjectorFS_hasCurrentPlatform(t *testing.T) {
	t.Parallel()

	_, err := injectorEntry(injectorFS, runtime.GOOS, runtime.GOARCH)
	require.NoError(t, err, "a placeholder must exist for %s/%s", runtime.GOOS, runtime.GOARCH)
}
