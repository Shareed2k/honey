#!/usr/bin/env bash
# Build the interception injector library for each target OS/arch and place it
# under internal/intercept/injector/<goos>_<goarch>/, replacing the committed
# placeholder so the released honey binary embeds a real, loadable injector.
#
# The injector is the data-plane module's native shim; its C source lives in a
# cgo comment block in that module's injector/main.go. This mirrors the module's
# own Makefile compile flags exactly, per platform, so a release honey binary
# ships a working injector for every platform whose C toolchain is available on
# the builder.
#
# The HOST platform is always built (a failure there is fatal). Cross targets are
# built only when their compiler is present; a missing cross toolchain leaves the
# placeholder in place (that platform reports "no bundled injector" at runtime,
# which honey surfaces cleanly). The goreleaser release image ships the osxcross
# and mingw toolchains it already uses for the cgo honey build.
#
# Overridable via env: INJECTOR_REF (module tag, default below), INJECTOR_REPO,
# and per-target compilers (LINUX_AMD64_CC, LINUX_ARM64_CC, DARWIN_AMD64_CC,
# DARWIN_ARM64_CC).
set -euo pipefail

INJECTOR_REPO="${INJECTOR_REPO:-https://github.com/shareed2k/mogate}"
INJECTOR_REF="${INJECTOR_REF:-v0.1.5}"

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
embed_root="${repo_root}/internal/intercept/injector"
work_dir="${repo_root}/build/intercept-injector-src"

host_goos="$(go env GOHOSTOS)"
host_goarch="$(go env GOHOSTARCH)"

# Fetch (or refresh) the pinned data-plane source and generate its C header (the
# header is generated, not committed, so a fresh checkout lacks it).
if [ ! -d "${work_dir}/.git" ]; then
  rm -rf "${work_dir}"
  git clone --depth 1 --branch "${INJECTOR_REF}" "${INJECTOR_REPO}" "${work_dir}"
fi
( cd "${work_dir}" && go generate ./internal/protocol )

injector_main="${work_dir}/injector/main.go"
if [ ! -f "${injector_main}" ]; then
  echo "build-intercept-injector: injector source not found at ${injector_main}" >&2
  exit 1
fi

# Extract the C source from the cgo comment block (drop the /* */ fences and the
# #cgo lines), matching the data-plane module's Makefile.
extract_c() {
  sed -n '/^\/\*$/,/^\*\/$/p' "${injector_main}" | sed '1d;$d;/^#cgo /d'
}

# build_one <goos> <goarch> <cc> <ext> <shared_flags...>
build_one() {
  local goos="$1" goarch="$2" cc="$3" ext="$4"
  shift 4
  local shared_flags=("$@")
  local dest_dir="${embed_root}/${goos}_${goarch}"
  local is_host=false
  if [ "${goos}" = "${host_goos}" ] && [ "${goarch}" = "${host_goarch}" ]; then
    is_host=true
  fi

  # On a macOS host, both darwin slices build natively with `clang -arch` — no
  # osxcross required. This is what makes the x86_64 slice available on an
  # Apple-Silicon dev machine, which the SIP path needs: SIP-restricted system
  # binaries are thinned to x86_64 and run under Rosetta with the x86_64
  # injector. Prefer an explicitly provided cross compiler if present; otherwise
  # use the native clang with the target arch. Non-darwin builders fall through
  # to the cross-compiler logic below (the release image ships osxcross).
  if [ "${goos}" = "darwin" ] && [ "${host_goos}" = "darwin" ]; then
    local mach_arch="${goarch}"
    if [ "${goarch}" = "amd64" ]; then mach_arch="x86_64"; fi
    if ! command -v "${cc}" >/dev/null 2>&1; then
      cc="cc"
    fi
    shared_flags=("-arch" "${mach_arch}" "${shared_flags[@]}")
  elif ! command -v "${cc}" >/dev/null 2>&1; then
    # The host platform can always fall back to the native compiler (this is the
    # dev story: build the running platform's lib locally). Cross targets are
    # skipped when their toolchain is absent, leaving the placeholder.
    if [ "${is_host}" = true ] && command -v cc >/dev/null 2>&1; then
      cc="cc"
    elif [ "${is_host}" = true ]; then
      echo "build-intercept-injector: no compiler for host ${goos}/${goarch}" >&2
      return 1
    else
      echo "build-intercept-injector: skip ${goos}/${goarch} (compiler ${cc} not found)" >&2
      return 0
    fi
  fi

  mkdir -p "${dest_dir}"
  local out="${dest_dir}/injector.${ext}"
  extract_c | "${cc}" -x c -I"${work_dir}/injector" -O2 -fPIC "${shared_flags[@]}" -ldl -pthread -o "${out}" -
  # Drop the committed placeholder so extraction picks the real library.
  rm -f "${dest_dir}"/*.placeholder
  echo "build-intercept-injector: built ${out}"
}

# Non-host targets use an unambiguous cross-compiler (never a bare gcc/cc, which
# on a mac is clang and would silently emit a Mach-O for a "linux" target). Each
# host builds via its native cc through the host-fallback in build_one, so on the
# linux release builder linux/amd64 resolves to the native gcc (correct ELF).
build_one linux  amd64 "${LINUX_AMD64_CC:-x86_64-linux-gnu-gcc}"  so    -shared
build_one linux  arm64 "${LINUX_ARM64_CC:-aarch64-linux-gnu-gcc}" so    -shared
build_one darwin amd64 "${DARWIN_AMD64_CC:-o64-clang}"            dylib -dynamiclib -Wno-deprecated-declarations
build_one darwin arm64 "${DARWIN_ARM64_CC:-oa64-clang}"           dylib -dynamiclib -Wno-deprecated-declarations
