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
