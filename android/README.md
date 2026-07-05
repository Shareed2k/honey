# honey Android

Runs the `honey web` server as a local Android foreground service and provides a Jetpack Compose UI to it over `http://127.0.0.1:8765`.

## Architecture

```
Android app
  └─ HoneyService (foreground)
       └─ honey-arm64 binary  ← compiled from ../cmd/honey
            └─ honey web --listen 127.0.0.1:8765 --no-open
  └─ Retrofit → localhost:8765
  └─ Jetpack Compose UI (Dashboard · Backends · Exec · Recipes)
```

The binary is cross-compiled with `CGO_ENABLED=0 -tags mobile` (ONNX anomaly detection is stubbed — not available without CGO).

## Prerequisites

- Go 1.26+
- Android SDK (API 35 / `compileSdk 35`)
- JDK 17
- `ANDROID_HOME` set, or `sdk.dir` in `android/local.properties`

## Build

```bash
# From repo root — builds APK (runs the Go cross-compile automatically):
cd android && ./gradlew assembleDebug
```

Output: `android/app/build/outputs/apk/debug/app-debug.apk`

To cross-compile the Go binary separately:
```bash
bash scripts/build-android.sh
```

## Install & run

```bash
adb install android/app/build/outputs/apk/debug/app-debug.apk
```

Open the app. A persistent notification "Honey · Running on :8765" confirms the server is up. The Dashboard screen polls `/api/v1/meta` and turns green when honey is ready (allow 2–3 s).

## Configuration

honey looks for its config in the directory passed via `--config`. The service passes `<app files dir>/config/` (`/data/data/com.honey.mobile/files/config/`). To push a config:

```bash
adb push ~/.config/honey/config.yaml /data/data/com.honey.mobile/files/config/
adb shell am force-stop com.honey.mobile   # restart to reload
```

Alternatively, place any honey config (backends, providers, policies) in that path before first launch.

## Supported ABIs

Only `arm64-v8a` is supported without the Android NDK (all other ABIs require CGO for the runtime linker). Modern physical devices and ARM-based emulators (Apple Silicon Macs) work out of the box. x86_64 emulators on Intel Macs are not supported in this build.

## Notes

- The honey binary is ~190 MB (Go includes its runtime). A production release should add `-ldflags="-s -w"` (already in the build script) and consider UPX compression.
- The `HONEY_RISK_DISABLE=1` env var can be passed via `ProcessBuilder.environment()` in `HoneyProcess.kt` to bypass the risk gate during development.
