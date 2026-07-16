package com.honey.mobile.ui

import android.app.Activity
import android.content.Intent
import android.net.VpnService
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.core.content.ContextCompat
import androidx.fragment.app.FragmentActivity
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.honey.mobile.auth.KeyUnlock
import com.honey.mobile.data.KeyStoreManager
import com.honey.mobile.data.SshKeyMeta
import com.honey.mobile.ui.components.SshKeyDropdown
import com.honey.mobile.ui.theme.*
import com.honey.mobile.vpn.HoneyVpnService
import com.honey.mobile.vpn.VpnController
import com.honey.mobile.vpn.VpnRequest
import com.honey.mobile.vpn.VpnState
import com.honey.mobile.vpn.VpnStats
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import javax.inject.Inject
import java.util.Locale

@HiltViewModel
class VpnViewModel @Inject constructor(
    private val controller: VpnController,
    private val keyStore: KeyStoreManager,
) : ViewModel() {
    val state: StateFlow<VpnState> = controller.state

    private val _keys = MutableStateFlow<List<SshKeyMeta>>(emptyList())
    val keys = _keys.asStateFlow()

    init {
        viewModelScope.launch {
            _keys.value = withContext(Dispatchers.IO) { keyStore.list() }
        }
    }

    fun stage(request: VpnRequest) {
        controller.pendingRequest = request
    }

    fun keyNeedsPassphrasePrompt(keyId: String?): Boolean {
        val meta = keys.value.firstOrNull { it.id == keyId } ?: return false
        return meta.hasPassphrase && !meta.passphraseSaved
    }

    /**
     * Decrypts the selected SSH key, prompting for biometric/credential auth when
     * the key is sealed. Returns (pem, savedPassphrase) or null if not found or the
     * user cancelled (state is set to Error in that case).
     */
    suspend fun unlockKey(activity: FragmentActivity, keyId: String): Pair<String, String>? =
        when (val s = keyStore.beginUnlock(keyId)) {
            is KeyStoreManager.UnlockStart.Ready -> s.pem to s.passphrase
            is KeyStoreManager.UnlockStart.NeedAuth -> try {
                val c = KeyUnlock.authorize(activity, s.cipher, "Honey", "Unlock SSH key to connect")
                keyStore.completeUnlock(s, c)
            } catch (e: Exception) {
                controller.onError("Key unlock failed: ${e.message ?: "auth cancelled"}")
                null
            }
            KeyStoreManager.UnlockStart.NotFound -> {
                controller.onError("SSH key not found")
                null
            }
        }
}

@Composable
fun VpnScreen(
    prefilledExit: String = "",
    prefilledIp: String = "",
    prefilledSshPort: Int = 0,
    prefilledHoneyBackend: String = "",
    vm: VpnViewModel = hiltViewModel(),
) {
    val context = LocalContext.current
    val activity = context as FragmentActivity
    val scope = rememberCoroutineScope()
    val state by vm.state.collectAsState()
    val keys by vm.keys.collectAsState()

    var sshUser by remember { mutableStateOf("") }
    var selectedKeyId by remember { mutableStateOf<String?>(null) }
    var passphrasePrompt by remember { mutableStateOf(false) }
    var pendingPem by remember { mutableStateOf("") }

    fun startService() {
        val intent = Intent(context, HoneyVpnService::class.java).setAction(HoneyVpnService.ACTION_START)
        ContextCompat.startForegroundService(context, intent)
    }

    val consentLauncher = rememberLauncherForActivityResult(
        ActivityResultContracts.StartActivityForResult(),
    ) { result ->
        if (result.resultCode == Activity.RESULT_OK) startService()
    }

    fun connect(pem: String, passphrase: String) {
        vm.stage(
            VpnRequest(
                exitName = prefilledExit,
                hostIp = prefilledIp,
                sshPort = prefilledSshPort,
                sshUser = sshUser.trim(),
                sshKeyPem = pem,
                sshKeyPassphrase = passphrase,
            ),
        )
        val prepare = VpnService.prepare(context)
        if (prepare != null) consentLauncher.launch(prepare) else startService()
    }

    // Decrypt the key (biometric prompt when sealed), then prompt for the SSH
    // passphrase only if the key needs one that wasn't saved, then connect.
    fun unlockThenConnect() {
        val keyId = selectedKeyId
        scope.launch {
            val resolved = if (keyId == null) "" to "" else vm.unlockKey(activity, keyId) ?: return@launch
            val (pem, savedPass) = resolved
            if (keyId != null && vm.keyNeedsPassphrasePrompt(keyId) && savedPass.isEmpty()) {
                pendingPem = pem
                passphrasePrompt = true
            } else {
                connect(pem, savedPass)
            }
        }
    }

    fun disconnect() {
        val intent = Intent(context, HoneyVpnService::class.java).setAction(HoneyVpnService.ACTION_STOP)
        ContextCompat.startForegroundService(context, intent)
    }

    if (passphrasePrompt) {
        PassphraseDialog(
            onDismiss = { passphrasePrompt = false },
            onConfirm = { pass -> passphrasePrompt = false; connect(pendingPem, pass) },
        )
    }

    val ringState = when (state) {
        is VpnState.Connected -> RingState.Connected
        VpnState.Connecting, VpnState.Resolving -> RingState.Connecting
        is VpnState.Error -> RingState.Error
        VpnState.Disconnected -> RingState.Idle
    }
    val connected = state is VpnState.Connected
    val busy = state is VpnState.Connecting || state is VpnState.Resolving

    GradientBackground {
        Column(
            Modifier
                .fillMaxSize()
                .verticalScroll(rememberScrollState())
                .padding(horizontal = 20.dp)
                .padding(top = 24.dp, bottom = 32.dp),
            horizontalAlignment = Alignment.CenterHorizontally,
        ) {
            StatusRing(state = ringState) {
                Column(horizontalAlignment = Alignment.CenterHorizontally) {
                    Text(
                        statusLabel(state).uppercase(Locale.US),
                        style = MaterialTheme.typography.titleMedium,
                        color = when (ringState) {
                            RingState.Connected -> NeonCyan
                            RingState.Error -> NeonRed
                            else -> TextMid
                        },
                        fontWeight = FontWeight.Bold,
                    )
                    (state as? VpnState.Connected)?.let {
                        Spacer(Modifier.height(6.dp))
                        Text(
                            formatUptime(it.stats.uptimeSeconds),
                            style = MonoStyle.copy(fontSize = 22.sp),
                            color = TextHi,
                        )
                        Text(it.exit, color = TextMid, style = MaterialTheme.typography.bodySmall)
                    }
                }
            }

            Spacer(Modifier.height(24.dp))

            (state as? VpnState.Connected)?.let { ThroughputRow(it.stats) }
            (state as? VpnState.Error)?.let {
                Text(it.message, color = NeonRed, style = MaterialTheme.typography.bodyMedium)
            }

            Spacer(Modifier.height(20.dp))

            GlowCard(Modifier.fillMaxWidth()) {
                Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
                    Column {
                        Text("Exit host", color = TextDim, style = MaterialTheme.typography.labelSmall)
                        Spacer(Modifier.height(2.dp))
                        Text(prefilledExit, color = NeonCyan, style = MaterialTheme.typography.titleSmall)
                        if (prefilledIp.isNotEmpty()) {
                            Text(prefilledIp, color = TextMid, style = MaterialTheme.typography.bodySmall)
                        }
                    }
                    OutlinedTextField(
                        value = sshUser,
                        onValueChange = { sshUser = it },
                        label = { Text("SSH user (optional)") },
                        singleLine = true,
                        enabled = !connected && !busy,
                        modifier = Modifier.fillMaxWidth(),
                    )
                    if (prefilledHoneyBackend.isBlank()) {
                        SshKeyDropdown(
                            keys = keys,
                            selectedId = selectedKeyId,
                            enabled = !connected && !busy,
                            onSelect = { selectedKeyId = it },
                        )
                    } else {
                        Text(
                            "Routed via provider: $prefilledHoneyBackend (mTLS)",
                            color = TextDim,
                            style = MaterialTheme.typography.labelSmall,
                        )
                    }
                }
            }

            Spacer(Modifier.height(28.dp))

            if (connected || busy) {
                NeonButton(
                    onClick = { disconnect() },
                    danger = true,
                    modifier = Modifier.fillMaxWidth(),
                ) { Text(if (busy) "Cancel" else "Disconnect", fontWeight = FontWeight.Bold) }
            } else {
                NeonButton(
                    onClick = { unlockThenConnect() },
                    enabled = prefilledExit.isNotBlank(),
                    modifier = Modifier.fillMaxWidth(),
                ) { Text("Connect", fontWeight = FontWeight.Bold) }
            }
        }
    }
}

@Composable
private fun ThroughputRow(stats: VpnStats) {
    Row(
        Modifier.fillMaxWidth(),
        horizontalArrangement = Arrangement.SpaceEvenly,
    ) {
        Metric("▲ UP", formatRate(stats.upRate), formatBytes(stats.upTotal))
        Metric("▼ DOWN", formatRate(stats.downRate), formatBytes(stats.downTotal))
    }
}

@Composable
private fun Metric(label: String, rate: String, total: String) {
    Column(horizontalAlignment = Alignment.CenterHorizontally) {
        Text(label, color = TextDim, style = MaterialTheme.typography.labelSmall)
        Text(rate, style = MonoStyle.copy(fontSize = 18.sp), color = NeonCyan)
        Text(total, style = MonoStyle.copy(fontSize = 12.sp), color = TextMid)
    }
}

@Composable
private fun PassphraseDialog(onDismiss: () -> Unit, onConfirm: (String) -> Unit) {
    var pass by remember { mutableStateOf("") }
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("Key passphrase") },
        text = {
            OutlinedTextField(
                value = pass,
                onValueChange = { pass = it },
                label = { Text("Passphrase") },
                singleLine = true,
                modifier = Modifier.fillMaxWidth(),
            )
        },
        confirmButton = { TextButton(onClick = { onConfirm(pass) }) { Text("Connect") } },
        dismissButton = { TextButton(onClick = onDismiss) { Text("Cancel") } },
    )
}

private fun statusLabel(state: VpnState): String = when (state) {
    VpnState.Disconnected -> "Disconnected"
    VpnState.Resolving -> "Resolving"
    VpnState.Connecting -> "Connecting"
    is VpnState.Connected -> "Connected"
    is VpnState.Error -> "Error"
}

private fun formatUptime(seconds: Long): String {
    val h = seconds / 3600
    val m = (seconds % 3600) / 60
    val s = seconds % 60
    return String.format(Locale.US, "%02d:%02d:%02d", h, m, s)
}

private fun formatBytes(bytes: Long): String {
    if (bytes < 1024) return "$bytes B"
    val units = arrayOf("KB", "MB", "GB", "TB")
    var v = bytes.toDouble() / 1024
    var i = 0
    while (v >= 1024 && i < units.size - 1) { v /= 1024; i++ }
    return String.format(Locale.US, "%.1f %s", v, units[i])
}

private fun formatRate(bytesPerSec: Long): String = formatBytes(bytesPerSec) + "/s"
