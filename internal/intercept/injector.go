package intercept

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
)

// injectorFS embeds the per-platform interception injector libraries, one
// subdirectory per GOOS_GOARCH. The committed entries are placeholders; the
// release build replaces them with the real libraries.
//
//go:embed injector/*/*
var injectorFS embed.FS

// ErrNoInjector reports that no interception injector library is bundled for
// the running platform. The caller should surface a clear message; on some
// platforms local interception may additionally be blocked by the operating
// system's integrity protection.
var ErrNoInjector = errors.New("intercept: no bundled injector for this platform")

// injectorRoot is the directory within the embedded FS that holds the
// per-platform injector subdirectories.
const injectorRoot = "injector"

// injectorFileName is the basename the extracted injector library is written
// under inside the destination directory.
const injectorFileName = "injector.lib"

// injectorRosettaFileName is the basename the extracted x86_64 injector is
// written under. It is kept distinct from injectorFileName so a darwin session
// can extract both the native (arm64) and the x86_64 injector into the same
// directory: the x86_64 one is loaded when a SIP-patched binary is thinned to
// its x86_64 slice and run under Rosetta.
const injectorRosettaFileName = "injector-x86_64.lib"

// extractInjector writes the injector library for the running platform into dir
// at mode 0700 and returns its path. It returns ErrNoInjector when no library
// is bundled for the current GOOS/GOARCH.
func extractInjector(dir string) (string, error) {
	return extractInjectorFor(injectorFS, runtime.GOOS, runtime.GOARCH, dir)
}

// ExtractInjector is the exported form of extractInjector. It exists so the
// CLI's brokered intercept path (internal/cli/intercept.go) can build its own
// local.Config and run the local injection session directly — the agent in
// that flow is deployed server-side by the Broker, so the CLI never
// constructs a Session and has no other way to reach the embedded injector.
func ExtractInjector(dir string) (string, error) {
	return extractInjector(dir)
}

// ExtractRosettaInjector writes the x86_64 (darwin/amd64) injector into dir and
// returns its path. It is used on Apple Silicon for the SIP path: a
// SIP-restricted system binary is thinned to its x86_64 slice and run under
// Rosetta, which loads the x86_64 injector rather than the native arm64 one. It
// returns ErrNoInjector when no real x86_64 injector is bundled (for example a
// build that did not compile the darwin/amd64 slice); callers treat that as an
// empty InjectorLibRosetta so the SIP x86_64 path fails loud at use rather than
// loading a placeholder.
func ExtractRosettaInjector(dir string) (string, error) {
	return extractInjectorForNamed(injectorFS, "darwin", "amd64", dir, injectorRosettaFileName)
}

// extractInjectorFor is the platform-parameterised core of extractInjector: it
// reads the injector library for goos/goarch from fsys and writes it into dir
// at mode 0700, returning the written path. Factoring the platform out makes
// the not-found path testable without pretending to run on another OS.
func extractInjectorFor(fsys fs.FS, goos, goarch, dir string) (string, error) {
	return extractInjectorForNamed(fsys, goos, goarch, dir, injectorFileName)
}

// extractInjectorForNamed is extractInjectorFor with an explicit destination
// basename, so a single directory can hold more than one extracted injector
// (the native and the x86_64/Rosetta libraries) without colliding.
func extractInjectorForNamed(fsys fs.FS, goos, goarch, dir, filename string) (string, error) {
	entry, err := injectorEntry(fsys, goos, goarch)
	if err != nil {
		return "", err
	}
	data, err := fs.ReadFile(fsys, entry)
	if err != nil {
		return "", fmt.Errorf("intercept: read injector %q: %w", entry, err)
	}
	dest := filepath.Join(dir, filename)
	if err := os.WriteFile(dest, data, 0o600); err != nil {
		return "", fmt.Errorf("intercept: write injector: %w", err)
	}
	// Add the owner-execute bits so the library can be loaded. A computed mode
	// (rather than a literal) keeps the final permission at exactly 0700.
	info, err := os.Stat(dest)
	if err != nil {
		return "", fmt.Errorf("intercept: stat injector: %w", err)
	}
	if err := os.Chmod(dest, info.Mode().Perm()|0o700); err != nil {
		return "", fmt.Errorf("intercept: chmod injector: %w", err)
	}
	return dest, nil
}

// injectorEntry returns the embedded-FS path of the first real injector library
// in the goos_goarch subdirectory, or ErrNoInjector when that subdirectory is
// absent or holds no real library. A committed *.placeholder file is NOT a
// library: it only lets the //go:embed directive compile from a source checkout
// on a platform whose injector the current build did not compile. Serving it
// would write non-ELF/non-Mach-O bytes that the loader rejects with a cryptic
// "invalid ELF header"; skipping it instead makes the caller report the intended
// clean "no bundled injector for this platform" error.
func injectorEntry(fsys fs.FS, goos, goarch string) (string, error) {
	plat := goos + "_" + goarch
	dir := path.Join(injectorRoot, plat)
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return "", fmt.Errorf("%w: %s", ErrNoInjector, plat)
	}
	for _, e := range entries {
		if e.IsDir() || strings.HasSuffix(e.Name(), ".placeholder") {
			continue
		}
		return path.Join(dir, e.Name()), nil
	}
	return "", fmt.Errorf("%w: %s", ErrNoInjector, plat)
}
