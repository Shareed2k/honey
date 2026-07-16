package com.honey.mobile.ui

import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Computer
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import androidx.fragment.app.FragmentActivity
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import com.honey.mobile.api.ExecRequest
import com.honey.mobile.api.ExecResult
import com.honey.mobile.api.HoneyApi
import com.honey.mobile.auth.KeyUnlock
import com.honey.mobile.data.KeyStoreManager
import com.honey.mobile.data.SecretsStore
import com.honey.mobile.data.SshKeyMeta
import com.honey.mobile.ui.components.SshKeyDropdown
import com.honey.mobile.ui.theme.GlowCard
import com.honey.mobile.ui.theme.GradientBackground
import com.honey.mobile.ui.theme.MonoStyle
import com.honey.mobile.ui.theme.NeonButton
import com.honey.mobile.ui.theme.NeonCyan
import com.honey.mobile.ui.theme.NeonGreen
import com.honey.mobile.ui.theme.NeonRed
import com.honey.mobile.ui.theme.NeonViolet
import com.honey.mobile.ui.theme.TextDim
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import javax.inject.Inject

@HiltViewModel
class ExecViewModel @Inject constructor(
    private val api: HoneyApi,
    private val secrets: SecretsStore,
    private val keyStore: KeyStoreManager,
) : ViewModel() {
    private val _results = MutableStateFlow<List<ExecResult>>(emptyList())
    val results = _results.asStateFlow()
    private val _running = MutableStateFlow(false)
    val running = _running.asStateFlow()
    private val _keys = MutableStateFlow<List<SshKeyMeta>>(emptyList())
    val keys = _keys.asStateFlow()

    init {
        viewModelScope.launch {
            _keys.value = withContext(Dispatchers.IO) { keyStore.list() }
        }
    }

    /**
     * Decrypts the selected SSH key (biometric prompt when sealed) then runs
     * [command] directly on the host resolved from the dashboard record.
     */
    fun run(
        activity: FragmentActivity,
        host: String,
        hostIp: String,
        sshPort: Int,
        command: String,
        keyId: String?,
        sshUser: String = "",
    ) {
        viewModelScope.launch {
            _running.value = true
            _results.value = emptyList()
            val key = unlockKey(activity, keyId)
            if (key == null) {
                _running.value = false
                return@launch
            }
            val (pem, pass) = key
            val resolved = secrets.resolve(command)
            runCatching {
                _results.value = api.exec(
                    ExecRequest(
                        backends = emptyList(),
                        command = resolved,
                        name = host,
                        hostIp = hostIp,
                        sshPort = sshPort,
                        sshUser = sshUser,
                        sshIdentityFile = pem,
                        sshIdentityPassphrase = pass,
                    ),
                )
            }.onFailure { e ->
                _results.value = listOf(ExecResult(host = "error", output = e.message ?: "failed", exit_code = -1))
            }
            _running.value = false
        }
    }

    private fun execError(msg: String) {
        _results.value = listOf(ExecResult(host = "error", output = msg, exit_code = -1))
    }

    /** Returns (pem, passphrase), or null if not found / the unlock was cancelled. */
    private suspend fun unlockKey(activity: FragmentActivity, keyId: String?): Pair<String, String>? {
        if (keyId == null) return "" to ""
        return when (val s = keyStore.beginUnlock(keyId)) {
            is KeyStoreManager.UnlockStart.Ready -> s.pem to s.passphrase
            is KeyStoreManager.UnlockStart.NeedAuth -> try {
                val c = KeyUnlock.authorize(activity, s.cipher, "Honey", "Unlock SSH key to run")
                keyStore.completeUnlock(s, c)
            } catch (e: Exception) {
                execError("Key unlock failed: ${e.message ?: "auth cancelled"}")
                null
            }
            KeyStoreManager.UnlockStart.NotFound -> {
                execError("SSH key not found")
                null
            }
        }
    }
}

@Composable
fun ExecScreen(
    prefilledHost: String = "",
    prefilledIp: String = "",
    prefilledProvider: String = "",
    prefilledSshPort: Int = 0,
    prefilledHoneyBackend: String = "",
    vm: ExecViewModel = hiltViewModel(),
) {
    val activity = LocalContext.current as FragmentActivity
    var command by remember { mutableStateOf("") }
    var sshUser by remember { mutableStateOf("") }
    var selectedKeyId by remember { mutableStateOf<String?>(null) }
    val results by vm.results.collectAsStateWithLifecycle()
    val running by vm.running.collectAsStateWithLifecycle()
    val keys by vm.keys.collectAsStateWithLifecycle()

    GradientBackground {
        Column(Modifier.fillMaxSize().padding(16.dp)) {
            GlowCard(modifier = Modifier.fillMaxWidth(), glow = NeonCyan) {
                Row(
                    modifier = Modifier.padding(horizontal = 12.dp, vertical = 8.dp),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    Icon(
                        Icons.Default.Computer,
                        contentDescription = null,
                        tint = NeonCyan,
                        modifier = Modifier.size(16.dp),
                    )
                    Spacer(Modifier.width(8.dp))
                    Text("Target: ", color = TextDim, style = MaterialTheme.typography.labelMedium)
                    Text(prefilledHost, color = NeonCyan, style = MaterialTheme.typography.labelMedium)
                    if (prefilledIp.isNotEmpty()) {
                        Spacer(Modifier.width(6.dp))
                        Text(prefilledIp, color = TextDim, style = MaterialTheme.typography.labelMedium)
                    }
                    if (prefilledProvider.isNotEmpty()) {
                        Spacer(Modifier.width(6.dp))
                        Text("[$prefilledProvider]", color = NeonViolet, style = MaterialTheme.typography.labelMedium)
                    }
                }
            }
            Spacer(Modifier.height(8.dp))
            OutlinedTextField(
                value = sshUser,
                onValueChange = { sshUser = it },
                label = { Text("SSH user") },
                singleLine = true,
                modifier = Modifier.fillMaxWidth(),
            )
            Spacer(Modifier.height(8.dp))
            OutlinedTextField(
                value = command,
                onValueChange = { command = it },
                label = { Text("Command") },
                modifier = Modifier.fillMaxWidth(),
            )
            Spacer(Modifier.height(8.dp))
            if (prefilledHoneyBackend.isBlank()) {
                SshKeyDropdown(
                    keys = keys,
                    selectedId = selectedKeyId,
                    onSelect = { selectedKeyId = it },
                )
            } else {
                Text(
                    "Routed via provider: $prefilledHoneyBackend (mTLS)",
                    color = TextDim,
                    style = MaterialTheme.typography.labelMedium,
                )
            }
            Spacer(Modifier.height(12.dp))
            NeonButton(
                onClick = {
                    vm.run(
                        activity = activity,
                        host = prefilledHost,
                        hostIp = prefilledIp,
                        sshPort = prefilledSshPort,
                        command = command,
                        keyId = selectedKeyId,
                        sshUser = sshUser,
                    )
                },
                enabled = !running && command.isNotBlank(),
                modifier = Modifier.fillMaxWidth(),
            ) {
                Text(if (running) "Running…" else "Run")
            }
            Spacer(Modifier.height(16.dp))
            LazyColumn(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                items(results) { r ->
                    GlowCard(
                        modifier = Modifier.fillMaxWidth(),
                        glow = if (r.exit_code == 0) NeonCyan else NeonRed,
                    ) {
                        Column(Modifier.padding(12.dp)) {
                            Text(r.host, style = MaterialTheme.typography.labelLarge, color = NeonCyan)
                            if (r.output.isNotBlank()) {
                                Spacer(Modifier.height(4.dp))
                                Text(
                                    r.output,
                                    style = MonoStyle.copy(fontSize = MaterialTheme.typography.bodySmall.fontSize),
                                )
                            }
                            if (r.error != null) {
                                Spacer(Modifier.height(4.dp))
                                Text(
                                    "Error: ${r.error}",
                                    style = MonoStyle.copy(fontSize = MaterialTheme.typography.bodySmall.fontSize),
                                    color = NeonRed,
                                )
                            }
                        }
                    }
                }
            }
        }
    }
}
