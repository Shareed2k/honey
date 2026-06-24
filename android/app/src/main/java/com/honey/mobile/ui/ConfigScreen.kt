package com.honey.mobile.ui

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.Delete
import androidx.compose.material.icons.filled.Edit
import androidx.compose.material.icons.filled.KeyboardArrowDown
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import com.honey.mobile.data.*
import com.honey.mobile.ui.components.ValidatedTextField
import com.honey.mobile.util.Validators
import java.util.UUID
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import javax.inject.Inject

@HiltViewModel
class ConfigViewModel @Inject constructor(
    private val repo: ConfigRepository,
    private val secrets: SecretsStore
) : ViewModel() {
    private val _config = MutableStateFlow(HoneyConfig())
    val config = _config.asStateFlow()

    val secretKeys: List<String> get() = secrets.keys().sorted()

    init {
        viewModelScope.launch(Dispatchers.IO) { _config.value = repo.load() }
    }

    fun saveDefaults(sshUser: String, cacheTtl: String) {
        val updated = _config.value.copy(
            defaults = _config.value.defaults.copy(sshUser = sshUser, cacheTtl = cacheTtl)
        )
        viewModelScope.launch(Dispatchers.IO) { repo.save(updated); _config.value = updated }
    }

    fun upsertBackend(item: Any) {
        viewModelScope.launch(Dispatchers.IO) {
            val updated = when (item) {
                is LocalBackendConfig   -> replaceOrAddLocal(_config.value, item)
                is K8sBackendConfig     -> replaceOrAddK8s(_config.value, item)
                is AwsBackendConfig     -> replaceOrAddAws(_config.value, item)
                is GcpBackendConfig     -> replaceOrAddGcp(_config.value, item)
                is ConsulBackendConfig  -> replaceOrAddConsul(_config.value, item)
                is ProxmoxBackendConfig -> replaceOrAddProxmox(_config.value, item)
                is TrueNasBackendConfig -> replaceOrAddTrueNas(_config.value, item)
                is DockerBackendConfig  -> replaceOrAddDocker(_config.value, item)
                else -> _config.value
            }
            repo.save(updated); _config.value = updated
        }
    }

    fun deleteBackend(type: String, name: String) {
        viewModelScope.launch(Dispatchers.IO) {
            val b = _config.value.backends
            val updated = _config.value.copy(backends = when (type) {
                "local"      -> b.copy(local = b.local.filterNot { it.name == name })
                "kubernetes" -> b.copy(kubernetes = b.kubernetes.filterNot { it.name == name })
                "aws"        -> b.copy(aws = b.aws.filterNot { it.name == name })
                "gcp"        -> b.copy(gcp = b.gcp.filterNot { it.name == name })
                "consul"     -> b.copy(consul = b.consul.filterNot { it.name == name })
                "proxmox"    -> b.copy(proxmox = b.proxmox.filterNot { it.name == name })
                "truenas"    -> b.copy(truenas = b.truenas.filterNot { it.name == name })
                "docker"     -> b.copy(docker = b.docker.filterNot { it.name == name })
                else -> b
            })
            repo.save(updated); _config.value = updated
        }
    }

    fun flatBackends(cfg: HoneyConfig): List<BackendItem> = buildList {
        cfg.backends.local.forEach      { add(BackendItem("local",      it.name, "${it.hosts.size} hosts")) }
        cfg.backends.kubernetes.forEach { add(BackendItem("kubernetes", it.name, it.context)) }
        cfg.backends.aws.forEach        { add(BackendItem("aws",        it.name, it.region)) }
        cfg.backends.gcp.forEach        { add(BackendItem("gcp",        it.name, it.project)) }
        cfg.backends.consul.forEach     { add(BackendItem("consul",     it.name, it.addr)) }
        cfg.backends.proxmox.forEach    { add(BackendItem("proxmox",    it.name, it.url)) }
        cfg.backends.truenas.forEach    { add(BackendItem("truenas",    it.name, it.url)) }
        cfg.backends.docker.forEach     { add(BackendItem("docker",     it.name, it.host.ifBlank { it.socket })) }
    }

    private fun replaceOrAddLocal(cfg: HoneyConfig, item: LocalBackendConfig) =
        cfg.copy(backends = cfg.backends.copy(local = cfg.backends.local.filterNot { it.name == item.name } + item))
    private fun replaceOrAddK8s(cfg: HoneyConfig, item: K8sBackendConfig) =
        cfg.copy(backends = cfg.backends.copy(kubernetes = cfg.backends.kubernetes.filterNot { it.name == item.name } + item))
    private fun replaceOrAddAws(cfg: HoneyConfig, item: AwsBackendConfig) =
        cfg.copy(backends = cfg.backends.copy(aws = cfg.backends.aws.filterNot { it.name == item.name } + item))
    private fun replaceOrAddGcp(cfg: HoneyConfig, item: GcpBackendConfig) =
        cfg.copy(backends = cfg.backends.copy(gcp = cfg.backends.gcp.filterNot { it.name == item.name } + item))
    private fun replaceOrAddConsul(cfg: HoneyConfig, item: ConsulBackendConfig) =
        cfg.copy(backends = cfg.backends.copy(consul = cfg.backends.consul.filterNot { it.name == item.name } + item))
    private fun replaceOrAddProxmox(cfg: HoneyConfig, item: ProxmoxBackendConfig) =
        cfg.copy(backends = cfg.backends.copy(proxmox = cfg.backends.proxmox.filterNot { it.name == item.name } + item))
    private fun replaceOrAddTrueNas(cfg: HoneyConfig, item: TrueNasBackendConfig) =
        cfg.copy(backends = cfg.backends.copy(truenas = cfg.backends.truenas.filterNot { it.name == item.name } + item))
    private fun replaceOrAddDocker(cfg: HoneyConfig, item: DockerBackendConfig) =
        cfg.copy(backends = cfg.backends.copy(docker = cfg.backends.docker.filterNot { it.name == item.name } + item))
}

// ---------------------------------------------------------------------------
// Screen
// ---------------------------------------------------------------------------

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ConfigScreen(vm: ConfigViewModel = hiltViewModel()) {
    val config by vm.config.collectAsStateWithLifecycle()
    var selectedTab by remember { mutableIntStateOf(0) }
    val tabs = listOf("Backends", "Defaults")

    var showTypePicker by remember { mutableStateOf(false) }
    var editingItem by remember { mutableStateOf<Any?>(null) }
    var editingType by remember { mutableStateOf("") }

    if (showTypePicker) {
        BackendTypePickerDialog(
            onDismiss = { showTypePicker = false },
            onSelect = { type ->
                editingType = type
                editingItem = null
                showTypePicker = false
            }
        )
    }

    if (editingType.isNotEmpty()) {
        BackendFormDialog(
            type = editingType,
            initial = editingItem,
            secretKeys = vm.secretKeys,
            onDismiss = { editingType = ""; editingItem = null },
            onSave = { item -> vm.upsertBackend(item); editingType = ""; editingItem = null }
        )
    }

    Scaffold(
        floatingActionButton = {
            if (selectedTab == 0) {
                FloatingActionButton(onClick = { showTypePicker = true }) {
                    Icon(Icons.Default.Add, contentDescription = "Add backend")
                }
            }
        }
    ) { padding ->
        Column(Modifier.fillMaxSize().padding(padding)) {
            TabRow(selectedTabIndex = selectedTab) {
                tabs.forEachIndexed { i, title ->
                    Tab(selected = selectedTab == i, onClick = { selectedTab = i }, text = { Text(title) })
                }
            }
            when (selectedTab) {
                0 -> BackendsTab(
                    items = vm.flatBackends(config),
                    onDelete = { vm.deleteBackend(it.type, it.name) },
                    onEdit = { item ->
                        val found = findBackend(config, item.type, item.name)
                        if (found != null) { editingItem = found; editingType = item.type }
                    }
                )
                1 -> DefaultsTab(
                    defaults = config.defaults,
                    onSave = { ssh, ttl -> vm.saveDefaults(ssh, ttl) }
                )
            }
        }
    }
}

// ---------------------------------------------------------------------------
// Backends tab
// ---------------------------------------------------------------------------

@Composable
private fun BackendsTab(
    items: List<BackendItem>,
    onDelete: (BackendItem) -> Unit,
    onEdit: (BackendItem) -> Unit
) {
    if (items.isEmpty()) {
        Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
            Text("No backends — tap + to add one")
        }
    } else {
        LazyColumn(Modifier.fillMaxSize()) {
            items(items, key = { "${it.type}:${it.name}" }) { item ->
                ListItem(
                    headlineContent = { Text(item.name) },
                    supportingContent = { Text("${item.type} · ${item.subtitle}") },
                    trailingContent = {
                        Row {
                            IconButton(onClick = { onEdit(item) }) {
                                Icon(Icons.Default.Edit, contentDescription = "Edit")
                            }
                            IconButton(onClick = { onDelete(item) }) {
                                Icon(Icons.Default.Delete, contentDescription = "Delete")
                            }
                        }
                    }
                )
                HorizontalDivider()
            }
        }
    }
}

// ---------------------------------------------------------------------------
// Defaults tab
// ---------------------------------------------------------------------------

@Composable
private fun DefaultsTab(defaults: ConfigDefaults, onSave: (String, String) -> Unit) {
    var sshUser by remember(defaults.sshUser) { mutableStateOf(defaults.sshUser) }
    var cacheTtl by remember(defaults.cacheTtl) { mutableStateOf(defaults.cacheTtl) }
    Column(
        Modifier.fillMaxWidth().padding(16.dp),
        verticalArrangement = Arrangement.spacedBy(12.dp)
    ) {
        OutlinedTextField(
            value = sshUser, onValueChange = { sshUser = it },
            label = { Text("SSH User") }, singleLine = true, modifier = Modifier.fillMaxWidth()
        )
        OutlinedTextField(
            value = cacheTtl, onValueChange = { cacheTtl = it },
            label = { Text("Cache TTL (e.g. 5m)") }, singleLine = true, modifier = Modifier.fillMaxWidth()
        )
        Button(onClick = { onSave(sshUser, cacheTtl) }, modifier = Modifier.fillMaxWidth()) {
            Text("Save Defaults")
        }
    }
}

// ---------------------------------------------------------------------------
// Type picker dialog
// ---------------------------------------------------------------------------

@Composable
private fun BackendTypePickerDialog(onDismiss: () -> Unit, onSelect: (String) -> Unit) {
    val types = listOf("local", "kubernetes", "aws", "gcp", "consul", "proxmox", "truenas", "docker")
    var selected by remember { mutableStateOf(types[0]) }
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("Backend Type") },
        text = {
            LazyColumn {
                items(types) { type ->
                    Row(
                        Modifier.fillMaxWidth().padding(vertical = 4.dp),
                        verticalAlignment = Alignment.CenterVertically
                    ) {
                        RadioButton(selected = selected == type, onClick = { selected = type })
                        Spacer(Modifier.width(8.dp))
                        Text(type, modifier = Modifier.clickable { selected = type })
                    }
                }
            }
        },
        confirmButton = { TextButton(onClick = { onSelect(selected) }) { Text("Next") } },
        dismissButton = { TextButton(onClick = onDismiss) { Text("Cancel") } }
    )
}

// ---------------------------------------------------------------------------
// Backend form dialog (dispatcher)
// ---------------------------------------------------------------------------

@Composable
private fun BackendFormDialog(
    type: String,
    initial: Any?,
    secretKeys: List<String>,
    onDismiss: () -> Unit,
    onSave: (Any) -> Unit
) {
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(if (initial == null) "Add $type backend" else "Edit $type backend") },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                when (type) {
                    "local"      -> LocalForm(initial as? LocalBackendConfig, onSave, onDismiss)
                    "kubernetes" -> K8sForm(initial as? K8sBackendConfig, onSave, onDismiss)
                    "aws"        -> AwsForm(initial as? AwsBackendConfig, onSave, onDismiss)
                    "gcp"        -> GcpForm(initial as? GcpBackendConfig, onSave, onDismiss)
                    "consul"     -> ConsulForm(initial as? ConsulBackendConfig, secretKeys, onSave, onDismiss)
                    "proxmox"    -> ProxmoxForm(initial as? ProxmoxBackendConfig, secretKeys, onSave, onDismiss)
                    "truenas"    -> TrueNasForm(initial as? TrueNasBackendConfig, secretKeys, onSave, onDismiss)
                    "docker"     -> DockerForm(initial as? DockerBackendConfig, onSave, onDismiss)
                }
            }
        },
        confirmButton = {},
        dismissButton = { TextButton(onClick = onDismiss) { Text("Cancel") } }
    )
}

// ---------------------------------------------------------------------------
// Individual backend forms
// ---------------------------------------------------------------------------

data class HostFormState(
    val config: LocalHostConfig, 
    val nameError: String? = null, 
    val ipError: String? = null, 
    val id: String = UUID.randomUUID().toString()
)

@Composable
private fun LocalForm(
    initial: LocalBackendConfig?,
    onSave: (Any) -> Unit,
    onDismiss: () -> Unit
) {
    var name by remember { mutableStateOf(initial?.name ?: "") }
    var nameError by remember { mutableStateOf<String?>(null) }
    
    // Store hosts as a single state list
    var hosts by remember { 
        mutableStateOf(
            if (initial != null && initial.hosts.isNotEmpty()) {
                initial.hosts.map { HostFormState(it) }
            } else {
                listOf(HostFormState(LocalHostConfig("", "")))
            }
        )
    }

    ValidatedTextField(
        value = name, 
        onValueChange = { name = it; nameError = null },
        label = "Name *", 
        errorMessage = nameError
    )
    
    Spacer(Modifier.height(8.dp))
    Text("Hosts", style = MaterialTheme.typography.titleMedium)
    
    hosts.forEachIndexed { index, state ->
        key(state.id) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Column(modifier = Modifier.weight(1f)) {
                    ValidatedTextField(
                        value = state.config.name,
                        onValueChange = { newVal -> 
                            val newHosts = hosts.toMutableList()
                            newHosts[index] = state.copy(
                                config = state.config.copy(name = newVal),
                                nameError = null
                            )
                            hosts = newHosts
                        },
                        label = "Host Name *",
                        errorMessage = state.nameError
                    )
                    ValidatedTextField(
                        value = state.config.primaryIp,
                        onValueChange = { newVal -> 
                            val newHosts = hosts.toMutableList()
                            newHosts[index] = state.copy(
                                config = state.config.copy(primaryIp = newVal),
                                ipError = null
                            )
                            hosts = newHosts
                        },
                        label = "Primary IP / Target *",
                        errorMessage = state.ipError
                    )
                }
                IconButton(onClick = {
                    hosts = hosts.filterIndexed { i, _ -> i != index }
                }) {
                    Icon(Icons.Default.Delete, contentDescription = "Delete Host")
                }
            }
            Spacer(Modifier.height(4.dp))
        }
    }
    
    TextButton(onClick = {
        hosts = hosts + HostFormState(LocalHostConfig("", ""))
    }) { Text("+ Add Host") }

    Spacer(Modifier.height(8.dp))

    TextButton(
        onClick = {
            var isValid = true
            
            if (name.isBlank()) {
                nameError = "Name is required"
                isValid = false
            }
            
            val newHosts = hosts.map { state ->
                var hNameErr: String? = null
                var hIpErr: String? = null
                
                if (state.config.name.isBlank()) { 
                    hNameErr = "Host Name is required"
                    isValid = false 
                }
                
                if (state.config.primaryIp.isBlank()) { 
                    hIpErr = "IP/Target is required"
                    isValid = false 
                } else if (!Validators.isValidIp(state.config.primaryIp) && !Validators.isValidUrl(state.config.primaryIp)) {
                    hIpErr = "Invalid IP or Target"
                    isValid = false
                }
                
                state.copy(nameError = hNameErr, ipError = hIpErr)
            }
            hosts = newHosts

            if (isValid) {
                onSave(LocalBackendConfig(name = name, hosts = hosts.map { it.config }))
            }
        },
        modifier = Modifier.fillMaxWidth()
    ) { Text("Save") }
}

@Composable
private fun K8sForm(
    initial: K8sBackendConfig?,
    onSave: (Any) -> Unit,
    onDismiss: () -> Unit
) {
    var name by remember { mutableStateOf(initial?.name ?: "") }
    var context by remember { mutableStateOf(initial?.context ?: "") }
    var kubeconfig by remember { mutableStateOf(initial?.kubeconfig ?: "") }
    var mode by remember { mutableStateOf(initial?.mode ?: "nodes") }

    var nameError by remember { mutableStateOf<String?>(null) }
    var contextError by remember { mutableStateOf<String?>(null) }

    ValidatedTextField(
        value = name, onValueChange = { name = it; nameError = null },
        label = "Name *", errorMessage = nameError
    )
    ValidatedTextField(
        value = context, onValueChange = { context = it; contextError = null },
        label = "Context *", errorMessage = contextError
    )
    ValidatedTextField(
        value = kubeconfig, onValueChange = { kubeconfig = it },
        label = "Kubeconfig path"
    )
    ValidatedTextField(
        value = mode, onValueChange = { mode = it },
        label = "Mode (nodes/pods)"
    )
    TextButton(
        onClick = {
            var isValid = true
            if (name.isBlank()) { nameError = "Name is required"; isValid = false }
            if (context.isBlank()) { contextError = "Context is required"; isValid = false }
            if (isValid) {
                onSave(K8sBackendConfig(name = name, context = context, kubeconfig = kubeconfig, mode = mode))
            }
        },
        modifier = Modifier.fillMaxWidth()
    ) { Text("Save") }
}

@Composable
private fun AwsForm(
    initial: AwsBackendConfig?,
    onSave: (Any) -> Unit,
    onDismiss: () -> Unit
) {
    var name by remember { mutableStateOf(initial?.name ?: "") }
    var profile by remember { mutableStateOf(initial?.profile ?: "") }
    var region by remember { mutableStateOf(initial?.region ?: "") }

    var nameError by remember { mutableStateOf<String?>(null) }
    var regionError by remember { mutableStateOf<String?>(null) }

    ValidatedTextField(
        value = name, onValueChange = { name = it; nameError = null },
        label = "Name *", errorMessage = nameError
    )
    ValidatedTextField(
        value = profile, onValueChange = { profile = it },
        label = "AWS Profile"
    )
    ValidatedTextField(
        value = region, onValueChange = { region = it; regionError = null },
        label = "Region *", errorMessage = regionError
    )
    TextButton(
        onClick = {
            var isValid = true
            if (name.isBlank()) { nameError = "Name is required"; isValid = false }
            if (region.isBlank()) { regionError = "Region is required"; isValid = false }
            if (isValid) {
                onSave(AwsBackendConfig(name = name, profile = profile, region = region))
            }
        },
        modifier = Modifier.fillMaxWidth()
    ) { Text("Save") }
}

@Composable
private fun GcpForm(
    initial: GcpBackendConfig?,
    onSave: (Any) -> Unit,
    onDismiss: () -> Unit
) {
    var name by remember { mutableStateOf(initial?.name ?: "") }
    var project by remember { mutableStateOf(initial?.project ?: "") }
    var zone by remember { mutableStateOf(initial?.zone ?: "") }

    var nameError by remember { mutableStateOf<String?>(null) }
    var projectError by remember { mutableStateOf<String?>(null) }

    ValidatedTextField(
        value = name, onValueChange = { name = it; nameError = null },
        label = "Name *", errorMessage = nameError
    )
    ValidatedTextField(
        value = project, onValueChange = { project = it; projectError = null },
        label = "GCP Project *", errorMessage = projectError
    )
    ValidatedTextField(
        value = zone, onValueChange = { zone = it },
        label = "Zone"
    )
    TextButton(
        onClick = {
            var isValid = true
            if (name.isBlank()) { nameError = "Name is required"; isValid = false }
            if (project.isBlank()) { projectError = "Project is required"; isValid = false }
            if (isValid) {
                onSave(GcpBackendConfig(name = name, project = project, zone = zone))
            }
        },
        modifier = Modifier.fillMaxWidth()
    ) { Text("Save") }
}

@Composable
private fun ConsulForm(
    initial: ConsulBackendConfig?,
    secretKeys: List<String>,
    onSave: (Any) -> Unit,
    onDismiss: () -> Unit
) {
    var name by remember { mutableStateOf(initial?.name ?: "") }
    var addr by remember { mutableStateOf(initial?.addr ?: "") }
    var token by remember { mutableStateOf(initial?.token ?: "") }

    var nameError by remember { mutableStateOf<String?>(null) }
    var addrError by remember { mutableStateOf<String?>(null) }

    ValidatedTextField(
        value = name, onValueChange = { name = it; nameError = null },
        label = "Name *", errorMessage = nameError
    )
    ValidatedTextField(
        value = addr, onValueChange = { addr = it; addrError = null },
        label = "Address (e.g. 127.0.0.1:8500) *", errorMessage = addrError,
        keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri)
    )
    SecretKeyPicker(
        label = "Token",
        value = token,
        secretKeys = secretKeys,
        onValueChange = { token = it }
    )
    TextButton(
        onClick = {
            var isValid = true
            if (name.isBlank()) { nameError = "Name is required"; isValid = false }
            if (addr.isBlank()) { 
                addrError = "Address is required"
                isValid = false 
            } else if (!Validators.isValidUrl(addr) && !Validators.isValidIp(addr.substringBefore(":"))) {
                addrError = "Invalid Address"
                isValid = false
            }
            if (isValid) {
                onSave(ConsulBackendConfig(name = name, addr = addr, token = token))
            }
        },
        modifier = Modifier.fillMaxWidth()
    ) { Text("Save") }
}

@Composable
private fun ProxmoxForm(
    initial: ProxmoxBackendConfig?,
    secretKeys: List<String>,
    onSave: (Any) -> Unit,
    onDismiss: () -> Unit
) {
    var name by remember { mutableStateOf(initial?.name ?: "") }
    var url by remember { mutableStateOf(initial?.url ?: "") }
    var user by remember { mutableStateOf(initial?.user ?: "") }
    var password by remember { mutableStateOf(initial?.password ?: "") }

    var nameError by remember { mutableStateOf<String?>(null) }
    var urlError by remember { mutableStateOf<String?>(null) }
    var userError by remember { mutableStateOf<String?>(null) }

    ValidatedTextField(
        value = name, onValueChange = { name = it; nameError = null },
        label = "Name *", errorMessage = nameError
    )
    ValidatedTextField(
        value = url, onValueChange = { url = it; urlError = null },
        label = "URL *", errorMessage = urlError,
        keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri)
    )
    ValidatedTextField(
        value = user, onValueChange = { user = it; userError = null },
        label = "User *", errorMessage = userError
    )
    SecretKeyPicker(
        label = "Password",
        value = password,
        secretKeys = secretKeys,
        onValueChange = { password = it }
    )
    TextButton(
        onClick = {
            var isValid = true
            if (name.isBlank()) { nameError = "Name is required"; isValid = false }
            if (url.isBlank()) {
                urlError = "URL is required"
                isValid = false
            } else if (!Validators.isValidUrl(url)) {
                urlError = "Invalid URL"
                isValid = false
            }
            if (user.isBlank()) { userError = "User is required"; isValid = false }
            if (isValid) {
                onSave(ProxmoxBackendConfig(name = name, url = url, user = user, password = password))
            }
        },
        modifier = Modifier.fillMaxWidth()
    ) { Text("Save") }
}

@Composable
private fun TrueNasForm(
    initial: TrueNasBackendConfig?,
    secretKeys: List<String>,
    onSave: (Any) -> Unit,
    onDismiss: () -> Unit
) {
    var name by remember { mutableStateOf(initial?.name ?: "") }
    var url by remember { mutableStateOf(initial?.url ?: "") }
    var username by remember { mutableStateOf(initial?.username ?: "") }
    var apiKey by remember { mutableStateOf(initial?.apiKey ?: "") }

    var nameError by remember { mutableStateOf<String?>(null) }
    var urlError by remember { mutableStateOf<String?>(null) }
    var usernameError by remember { mutableStateOf<String?>(null) }

    ValidatedTextField(
        value = name, onValueChange = { name = it; nameError = null },
        label = "Name *", errorMessage = nameError
    )
    ValidatedTextField(
        value = url, onValueChange = { url = it; urlError = null },
        label = "URL *", errorMessage = urlError,
        keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri)
    )
    ValidatedTextField(
        value = username, onValueChange = { username = it; usernameError = null },
        label = "Username *", errorMessage = usernameError
    )
    SecretKeyPicker(
        label = "API Key",
        value = apiKey,
        secretKeys = secretKeys,
        onValueChange = { apiKey = it }
    )
    TextButton(
        onClick = {
            var isValid = true
            if (name.isBlank()) { nameError = "Name is required"; isValid = false }
            if (url.isBlank()) {
                urlError = "URL is required"
                isValid = false
            } else if (!Validators.isValidUrl(url)) {
                urlError = "Invalid URL"
                isValid = false
            }
            if (username.isBlank()) { usernameError = "Username is required"; isValid = false }
            if (isValid) {
                onSave(TrueNasBackendConfig(name = name, url = url, username = username, apiKey = apiKey))
            }
        },
        modifier = Modifier.fillMaxWidth()
    ) { Text("Save") }
}

@Composable
private fun DockerForm(
    initial: DockerBackendConfig?,
    onSave: (Any) -> Unit,
    onDismiss: () -> Unit
) {
    var name by remember { mutableStateOf(initial?.name ?: "") }
    var host by remember { mutableStateOf(initial?.host ?: "") }
    var socket by remember { mutableStateOf(initial?.socket ?: "") }

    var nameError by remember { mutableStateOf<String?>(null) }

    ValidatedTextField(
        value = name, onValueChange = { name = it; nameError = null },
        label = "Name *", errorMessage = nameError
    )
    ValidatedTextField(
        value = host, onValueChange = { host = it },
        label = "Host (tcp://…)",
        keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri)
    )
    ValidatedTextField(
        value = socket, onValueChange = { socket = it },
        label = "Socket path"
    )
    TextButton(
        onClick = {
            var isValid = true
            if (name.isBlank()) { nameError = "Name is required"; isValid = false }
            // Bypass strict validation for host since docker expects schemes like tcp:// or unix://
            if (isValid) {
                onSave(DockerBackendConfig(name = name, host = host, socket = socket))
            }
        },
        modifier = Modifier.fillMaxWidth()
    ) { Text("Save") }
}

// ---------------------------------------------------------------------------
// SecretKeyPicker
// ---------------------------------------------------------------------------

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SecretKeyPicker(
    label: String,
    value: String,
    secretKeys: List<String>,
    errorMessage: String? = null,
    onValueChange: (String) -> Unit
) {
    var expanded by remember { mutableStateOf(false) }
    var manualMode by remember {
        mutableStateOf(secretKeys.isEmpty() || (!value.startsWith("{{") && value.isNotEmpty()))
    }
    var manualValue by remember { mutableStateOf(if (!value.startsWith("{{")) value else "") }

    if (manualMode || secretKeys.isEmpty()) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            OutlinedTextField(
                value = manualValue,
                onValueChange = { manualValue = it; onValueChange(it) },
                label = { Text(label) },
                singleLine = true,
                modifier = Modifier.weight(1f),
                isError = errorMessage != null,
                supportingText = errorMessage?.let { { Text(it) } }
            )
            if (secretKeys.isNotEmpty()) {
                IconButton(onClick = { manualMode = false }) {
                    Icon(Icons.Default.KeyboardArrowDown, contentDescription = "Pick secret")
                }
            }
        }
    } else {
        ExposedDropdownMenuBox(expanded = expanded, onExpandedChange = { expanded = it }) {
            OutlinedTextField(
                value = value,
                onValueChange = {},
                readOnly = true,
                label = { Text(label) },
                trailingIcon = { ExposedDropdownMenuDefaults.TrailingIcon(expanded) },
                modifier = Modifier
                    .menuAnchor()
                    .fillMaxWidth(),
                isError = errorMessage != null,
                supportingText = errorMessage?.let { { Text(it) } }
            )
            ExposedDropdownMenu(expanded = expanded, onDismissRequest = { expanded = false }) {
                secretKeys.forEach { key ->
                    DropdownMenuItem(
                        text = { Text("{{$key}}") },
                        onClick = { onValueChange("{{$key}}"); expanded = false }
                    )
                }
                HorizontalDivider()
                DropdownMenuItem(
                    text = { Text("Enter manually…") },
                    onClick = { manualMode = true; expanded = false }
                )
            }
        }
    }
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

private fun findBackend(cfg: HoneyConfig, type: String, name: String): Any? = when (type) {
    "local"      -> cfg.backends.local.find { it.name == name }
    "kubernetes" -> cfg.backends.kubernetes.find { it.name == name }
    "aws"        -> cfg.backends.aws.find { it.name == name }
    "gcp"        -> cfg.backends.gcp.find { it.name == name }
    "consul"     -> cfg.backends.consul.find { it.name == name }
    "proxmox"    -> cfg.backends.proxmox.find { it.name == name }
    "truenas"    -> cfg.backends.truenas.find { it.name == name }
    "docker"     -> cfg.backends.docker.find { it.name == name }
    else         -> null
}
