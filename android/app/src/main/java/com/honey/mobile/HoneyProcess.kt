package com.honey.mobile

import android.content.Context
import android.os.Build
import java.io.File

class HoneyProcess(private val context: Context) {
    val port = 8765
    private var process: Process? = null

    fun start(configDir: File): Result<Unit> = runCatching {
        val binary = extractBinary()
        process = ProcessBuilder(
            binary.absolutePath, "web",
            "--listen", "127.0.0.1:$port",
            "--no-open"
        )
            .directory(configDir)
            .redirectErrorStream(true)
            .start()
    }

    fun stop() {
        process?.destroy()
        process = null
    }

    fun isAlive(): Boolean = process?.isAlive == true

    private fun extractBinary(): File {
        val assetName = when {
            Build.SUPPORTED_ABIS.contains("arm64-v8a") -> "honey-arm64"
            else -> "honey-arm64" // fallback — only arm64 binary is bundled
        }
        val dest = File(context.filesDir, "honey")
        context.assets.open(assetName).use { input ->
            dest.outputStream().use { output -> input.copyTo(output) }
        }
        dest.setExecutable(true, true)
        return dest
    }
}
