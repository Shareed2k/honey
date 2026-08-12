# Injector libraries

This directory holds the interception agent's injector library, one
subdirectory per target platform named `<GOOS>_<GOARCH>` (for example
`linux_amd64`). Each subdirectory contains a single library file that is
extracted and loaded locally at runtime to route the intercepted process's
traffic through the target pod.

The committed `lib.placeholder` files are placeholders only: they exist so the
`//go:embed injector/*/*` directive compiles from a plain source checkout. The
real per-platform libraries are built and copied into these subdirectories by
the release pipeline (they are not committed). A local developer build can
populate the entry for the running platform only.

## Building the real libraries

`scripts/build-intercept-injector.sh` (task `build-intercept-injector`) clones
the pinned data-plane module, generates its C header, and compiles the injector
shared library for each target platform into `injector/<GOOS>_<GOARCH>/`,
replacing that platform's placeholder. The goreleaser `before` hooks run it so
released binaries embed a real injector.

- **Release:** the linux release builder ships the cross toolchains (osxcross,
  gcc multiarch) so every target platform's library is produced.
- **Dev:** run the task on your machine to populate the running platform's entry
  via the native compiler; cross targets are skipped when their toolchain is
  absent (they keep the placeholder). The end-to-end test instead builds the
  host library directly and passes it via `Options.InjectorLib`, so it never
  depends on the embedded libraries.

Windows has no bundled injector (the data-plane shim targets linux and darwin);
`honey intercept` reports "no bundled injector for this platform" there.
