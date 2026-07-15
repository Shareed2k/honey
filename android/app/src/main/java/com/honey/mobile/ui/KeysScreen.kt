package com.honey.mobile.ui

import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.Delete
import androidx.compose.material.icons.outlined.Key
import androidx.compose.material.icons.outlined.Lock
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.fragment.app.FragmentActivity
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.honey.mobile.auth.KeyUnlock
import com.honey.mobile.data.KeyStoreManager
import com.honey.mobile.data.SshKeyMeta
import com.honey.mobile.ui.theme.GlowCard
import com.honey.mobile.ui.theme.GradientBackground
import com.honey.mobile.ui.theme.MonoStyle
import com.honey.mobile.ui.theme.NeonCyan
import com.honey.mobile.ui.theme.NeonViolet
import com.honey.mobile.ui.theme.TextDim
import com.honey.mobile.ui.theme.TextMid
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.receiveAsFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import javax.inject.Inject

@HiltViewModel
class KeysViewModel @Inject constructor(
    private val keyStore: KeyStoreManager,
) : ViewModel() {
    private val _keys = MutableStateFlow<List<SshKeyMeta>>(emptyList())
    val keys = _keys.asStateFlow()

    private val _busy = MutableStateFlow(false)
    val busy = _busy.asStateFlow()

    private val _events = Channel<String>(Channel.BUFFERED)
    val events = _events.receiveAsFlow()

    init { refresh() }

    fun refresh() {
        viewModelScope.launch {
            _keys.value = withContext(Dispatchers.IO) { keyStore.list() }
        }
    }

    fun importKey(
        activity: FragmentActivity,
        name: String,
        pem: String,
        passphrase: String,
        savePassphrase: Boolean,
    ) {
        viewModelScope.launch {
            _busy.value = true
            val prepared = withContext(Dispatchers.IO) {
                runCatching { keyStore.prepareImport(name.trim(), pem.trim(), passphrase, savePassphrase) }
            }
            prepared.onSuccess { p ->
                try {
                    val authed = KeyUnlock.authorize(
                        activity,
                        keyStore.encryptCipher(),
                        "Honey",
                        "Protect SSH key with your biometrics",
                    )
                    val meta = withContext(Dispatchers.IO) { keyStore.finishImport(p, authed) }
                    _events.send("Imported ${meta.name} (${meta.type})")
                    refresh()
                } catch (e: Exception) {
                    _events.send("Import cancelled: ${e.message ?: "auth failed"}")
                }
            }.onFailure {
                _events.send("Invalid key: ${it.message ?: "parse failed"}")
            }
            _busy.value = false
        }
    }

    fun delete(meta: SshKeyMeta) {
        viewModelScope.launch {
            withContext(Dispatchers.IO) { keyStore.delete(meta.id) }
            refresh()
        }
    }
}

@Composable
fun KeysScreen(vm: KeysViewModel = hiltViewModel()) {
    val activity = LocalContext.current as FragmentActivity
    val keys by vm.keys.collectAsState()
    val busy by vm.busy.collectAsState()
    var showImport by remember { mutableStateOf(false) }
    var pendingDelete by remember { mutableStateOf<SshKeyMeta?>(null) }
    val snackbar = remember { SnackbarHostState() }

    LaunchedEffect(Unit) {
        vm.events.collect { snackbar.showSnackbar(it) }
    }

    if (showImport) {
        ImportKeyDialog(
            busy = busy,
            onDismiss = { showImport = false },
            onImport = { name, pem, pass, save ->
                vm.importKey(activity, name, pem, pass, save)
                showImport = false
            },
        )
    }

    pendingDelete?.let { meta ->
        AlertDialog(
            onDismissRequest = { pendingDelete = null },
            title = { Text("Delete key?") },
            text = { Text("Remove \"${meta.name}\"? This cannot be undone.") },
            confirmButton = {
                TextButton(onClick = { vm.delete(meta); pendingDelete = null }) { Text("Delete") }
            },
            dismissButton = { TextButton(onClick = { pendingDelete = null }) { Text("Cancel") } },
        )
    }

    Scaffold(
        containerColor = MaterialTheme.colorScheme.background,
        snackbarHost = { SnackbarHost(snackbar) },
        floatingActionButton = {
            FloatingActionButton(
                onClick = { showImport = true },
                containerColor = NeonCyan,
            ) { Icon(Icons.Default.Add, contentDescription = "Import key") }
        },
    ) { padding ->
        GradientBackground(Modifier.padding(padding)) {
            if (keys.isEmpty()) {
                Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                    Column(horizontalAlignment = Alignment.CenterHorizontally) {
                        Icon(
                            Icons.Outlined.Key,
                            contentDescription = null,
                            tint = TextDim,
                            modifier = Modifier.size(48.dp),
                        )
                        Spacer(Modifier.height(12.dp))
                        Text("No SSH keys", color = TextMid)
                        Text("Tap + to import a private key", color = TextDim, style = MaterialTheme.typography.bodySmall)
                    }
                }
            } else {
                LazyColumn(
                    Modifier.fillMaxSize().padding(horizontal = 16.dp),
                    verticalArrangement = Arrangement.spacedBy(12.dp),
                    contentPadding = PaddingValues(vertical = 16.dp),
                ) {
                    items(keys, key = { it.id }) { meta ->
                        KeyRow(meta = meta, onDelete = { pendingDelete = meta })
                    }
                }
            }
        }
    }
}

@Composable
private fun KeyRow(meta: SshKeyMeta, onDelete: () -> Unit) {
    GlowCard(glow = NeonViolet) {
        Row(
            Modifier.fillMaxWidth().padding(16.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Icon(Icons.Outlined.Key, contentDescription = null, tint = NeonCyan)
            Spacer(Modifier.width(14.dp))
            Column(Modifier.weight(1f)) {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Text(
                        meta.name,
                        style = MaterialTheme.typography.titleMedium,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                        modifier = Modifier.weight(1f, fill = false),
                    )
                    Spacer(Modifier.width(8.dp))
                    AssistChip(
                        onClick = {},
                        enabled = false,
                        label = { Text(meta.type) },
                    )
                    if (meta.hasPassphrase) {
                        Spacer(Modifier.width(6.dp))
                        Icon(
                            Icons.Outlined.Lock,
                            contentDescription = "passphrase protected",
                            tint = if (meta.passphraseSaved) NeonCyan else TextDim,
                            modifier = Modifier.size(16.dp),
                        )
                    }
                }
                Spacer(Modifier.height(4.dp))
                Text(
                    meta.fingerprint,
                    style = MonoStyle.copy(fontSize = MaterialTheme.typography.bodySmall.fontSize),
                    color = TextDim,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
            IconButton(onClick = onDelete) {
                Icon(Icons.Default.Delete, contentDescription = "Delete", tint = TextMid)
            }
        }
    }
}

@Composable
private fun ImportKeyDialog(
    busy: Boolean,
    onDismiss: () -> Unit,
    onImport: (name: String, pem: String, passphrase: String, savePassphrase: Boolean) -> Unit,
) {
    var name by remember { mutableStateOf("") }
    var pem by remember { mutableStateOf("") }
    var passphrase by remember { mutableStateOf("") }
    var savePass by remember { mutableStateOf(false) }

    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("Import SSH key") },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
                OutlinedTextField(
                    value = name,
                    onValueChange = { name = it },
                    label = { Text("Name (optional)") },
                    singleLine = true,
                    modifier = Modifier.fillMaxWidth(),
                )
                OutlinedTextField(
                    value = pem,
                    onValueChange = { pem = it },
                    label = { Text("Private key (PEM)") },
                    placeholder = { Text("-----BEGIN OPENSSH PRIVATE KEY-----") },
                    minLines = 4,
                    maxLines = 8,
                    textStyle = MonoStyle.copy(fontSize = MaterialTheme.typography.bodySmall.fontSize),
                    modifier = Modifier.fillMaxWidth(),
                )
                OutlinedTextField(
                    value = passphrase,
                    onValueChange = { passphrase = it },
                    label = { Text("Passphrase (if encrypted)") },
                    singleLine = true,
                    modifier = Modifier.fillMaxWidth(),
                )
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Switch(checked = savePass, onCheckedChange = { savePass = it }, enabled = passphrase.isNotEmpty())
                    Spacer(Modifier.width(8.dp))
                    Text("Save passphrase", color = TextMid)
                }
            }
        },
        confirmButton = {
            TextButton(
                onClick = { onImport(name, pem, passphrase, savePass) },
                enabled = !busy && pem.isNotBlank(),
            ) { Text(if (busy) "Validating…" else "Import") }
        },
        dismissButton = { TextButton(onClick = onDismiss) { Text("Cancel") } },
    )
}
