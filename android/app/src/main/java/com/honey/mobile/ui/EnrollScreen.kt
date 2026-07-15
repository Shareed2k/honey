package com.honey.mobile.ui

import android.Manifest
import android.content.Context
import android.content.pm.PackageManager
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.camera.core.CameraSelector
import androidx.camera.core.ImageAnalysis
import androidx.camera.core.ImageProxy
import androidx.camera.core.Preview
import androidx.camera.lifecycle.ProcessCameraProvider
import androidx.camera.view.PreviewView
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Button
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.core.content.ContextCompat
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.viewModelScope
import com.google.mlkit.vision.barcode.BarcodeScanning
import com.google.mlkit.vision.barcode.common.Barcode
import com.google.mlkit.vision.common.InputImage
import com.honey.mobile.data.DeviceEnrollment
import dagger.hilt.android.lifecycle.HiltViewModel
import dagger.hilt.android.qualifiers.ApplicationContext
import kotlinx.coroutines.launch
import org.json.JSONObject
import java.util.concurrent.Executors
import javax.inject.Inject

@HiltViewModel
class EnrollViewModel @Inject constructor(
    @ApplicationContext private val ctx: Context,
) : ViewModel() {
    private val enrollment = DeviceEnrollment(ctx)

    var status by mutableStateOf(if (enrollment.isEnrolled()) "This device is enrolled." else "")
        private set
    var busy by mutableStateOf(false)
        private set

    /** enrollFromBootstrap parses a scanned/pasted bootstrap JSON and enrolls. */
    fun enrollFromBootstrap(json: String) {
        if (busy) return
        viewModelScope.launch {
            busy = true
            status = "Enrolling…"
            try {
                val o = JSONObject(json)
                enrollment.enroll(
                    enrollUrl = o.getString("enroll_url"),
                    code = o.getString("code"),
                    cn = o.optString("cn", "device"),
                    caPinPem = null, // TODO: pin ca_fingerprint for the bootstrap TLS channel
                )
                status = "Enrolled ✓ — device certificate issued."
            } catch (e: Exception) {
                status = "Failed: ${e.message}"
            } finally {
                busy = false
            }
        }
    }
}

@Composable
fun EnrollScreen(vm: EnrollViewModel = hiltViewModel()) {
    val context = LocalContext.current
    var pasted by remember { mutableStateOf("") }
    var scanning by remember { mutableStateOf(false) }

    val camPermission = rememberLauncherForActivityResult(
        ActivityResultContracts.RequestPermission(),
    ) { granted -> scanning = granted }

    Column(Modifier.fillMaxSize().padding(16.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
        Text("Device enrollment (mTLS)")
        Text(vm.status)

        Button(
            enabled = !vm.busy,
            onClick = {
                if (ContextCompat.checkSelfPermission(context, Manifest.permission.CAMERA)
                    == PackageManager.PERMISSION_GRANTED
                ) {
                    scanning = true
                } else {
                    camPermission.launch(Manifest.permission.CAMERA)
                }
            },
        ) { Text(if (scanning) "Scanning…" else "Scan enrollment QR") }

        if (scanning) {
            QrScanner(
                modifier = Modifier.fillMaxWidth().height(320.dp),
                onResult = { text ->
                    scanning = false
                    vm.enrollFromBootstrap(text)
                },
            )
        }

        Spacer(Modifier.height(8.dp))
        Text("Or paste the bootstrap JSON:")
        OutlinedTextField(
            value = pasted,
            onValueChange = { pasted = it },
            modifier = Modifier.fillMaxWidth(),
            label = { Text("bootstrap JSON") },
        )
        Button(enabled = !vm.busy && pasted.isNotBlank(), onClick = { vm.enrollFromBootstrap(pasted) }) {
            Text("Enroll")
        }
    }
}

@Composable
private fun QrScanner(modifier: Modifier, onResult: (String) -> Unit) {
    val context = LocalContext.current
    val lifecycleOwner = LocalLifecycleOwner.current
    val analysisExecutor = remember { Executors.newSingleThreadExecutor() }
    var handled by remember { mutableStateOf(false) }

    AndroidView(
        modifier = modifier,
        factory = { ctx ->
            val previewView = PreviewView(ctx)
            val providerFuture = ProcessCameraProvider.getInstance(ctx)
            providerFuture.addListener({
                val provider = providerFuture.get()
                val preview = Preview.Builder().build().also { it.setSurfaceProvider(previewView.surfaceProvider) }
                val scanner = BarcodeScanning.getClient()
                val analysis = ImageAnalysis.Builder()
                    .setBackpressureStrategy(ImageAnalysis.STRATEGY_KEEP_ONLY_LATEST)
                    .build()
                analysis.setAnalyzer(analysisExecutor) { proxy: ImageProxy ->
                    val media = proxy.image
                    if (media == null || handled) {
                        proxy.close()
                        return@setAnalyzer
                    }
                    val img = InputImage.fromMediaImage(media, proxy.imageInfo.rotationDegrees)
                    scanner.process(img)
                        .addOnSuccessListener { codes ->
                            val qr = codes.firstOrNull { it.format == Barcode.FORMAT_QR_CODE }
                            val value = qr?.rawValue
                            if (!handled && value != null) {
                                handled = true
                                onResult(value)
                            }
                        }
                        .addOnCompleteListener { proxy.close() }
                }
                provider.unbindAll()
                provider.bindToLifecycle(lifecycleOwner, CameraSelector.DEFAULT_BACK_CAMERA, preview, analysis)
            }, ContextCompat.getMainExecutor(ctx))
            previewView
        },
    )
}
