package intercept

import (
	"errors"
	"io/fs"
	"os"
	"path"
	"runtime"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractInjector_currentPlatform(t *testing.T) {
	t.Parallel()

	// Environment-tolerant smoke test: a release build has a real injector for
	// the host platform and must extract it (non-empty, mode 0700); a fresh
	// source checkout (and the unit-test CI, which does not build injectors) has
	// only the *.placeholder, which injectorEntry now skips, so extraction must
	// return ErrNoInjector rather than serve the placeholder to the loader.
	dir := t.TempDir()
	p, err := extractInjector(dir)
	if err != nil {
		assert.True(t, errors.Is(err, ErrNoInjector),
			"a placeholder-only host platform must yield ErrNoInjector, got %v", err)
		return
	}

	info, err := os.Stat(p)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), info.Mode().Perm(), "injector must be extracted at mode 0700")

	data, err := os.ReadFile(p)
	require.NoError(t, err)
	assert.NotEmpty(t, data, "extracted injector must not be empty")
}

func TestExtractInjectorFor_placeholderOnly(t *testing.T) {
	t.Parallel()

	// A platform subdirectory that exists but holds only the committed
	// placeholder (the state of any cross target the build did not compile) must
	// report no bundled injector, never hand the placeholder text to the loader.
	fsys := fstest.MapFS{
		"injector/linux_amd64/lib.placeholder": {Data: []byte("Placeholder for the injector library.\n")},
	}
	dir := t.TempDir()

	_, err := extractInjectorFor(fsys, "linux", "amd64", dir)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrNoInjector), "placeholder-only platform must yield ErrNoInjector")
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

	// The embed must carry a subdirectory for the host platform so //go:embed
	// compiles and a release build has somewhere to drop the real library. This
	// asserts the directory exists (fresh checkouts hold only the placeholder);
	// whether it holds a loadable library is environment-dependent and covered by
	// TestExtractInjector_currentPlatform.
	dir := path.Join(injectorRoot, runtime.GOOS+"_"+runtime.GOARCH)
	entries, err := fs.ReadDir(injectorFS, dir)
	require.NoError(t, err, "embedded injector dir must exist for %s/%s", runtime.GOOS, runtime.GOARCH)
	require.NotEmpty(t, entries, "embedded injector dir must not be empty for %s/%s", runtime.GOOS, runtime.GOARCH)
}
