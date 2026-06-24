package com.honey.mobile.ui

import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import com.honey.mobile.api.ExecRequest
import com.honey.mobile.api.ExecResult
import com.honey.mobile.api.HoneyApi
import com.honey.mobile.data.SecretsStore
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import javax.inject.Inject

@HiltViewModel
class ExecViewModel @Inject constructor(
    private val api: HoneyApi,
    private val secrets: SecretsStore
) : ViewModel() {
    private val _results = MutableStateFlow<List<ExecResult>>(emptyList())
    val results = _results.asStateFlow()
    private val _running = MutableStateFlow(false)
    val running = _running.asStateFlow()

    fun run(backends: List<String>, command: String) {
        viewModelScope.launch {
            _running.value = true
            _results.value = emptyList()
            val resolved = secrets.resolve(command)
            runCatching {
                _results.value = api.exec(ExecRequest(backends = backends, command = resolved))
            }.onFailure { e ->
                _results.value = listOf(ExecResult(host = "error", output = e.message ?: "failed", exit_code = -1))
            }
            _running.value = false
        }
    }
}

@Composable
fun ExecScreen(prefilledBackend: String = "", vm: ExecViewModel = hiltViewModel()) {
    var command by remember { mutableStateOf("") }
    var backend by remember { mutableStateOf(prefilledBackend) }
    val results by vm.results.collectAsStateWithLifecycle()
    val running by vm.running.collectAsStateWithLifecycle()

    Column(Modifier.fillMaxSize().padding(16.dp)) {
        OutlinedTextField(
            value = backend,
            onValueChange = { backend = it },
            label = { Text("Backend pattern (e.g. prod-*)") },
            modifier = Modifier.fillMaxWidth()
        )
        Spacer(Modifier.height(8.dp))
        OutlinedTextField(
            value = command,
            onValueChange = { command = it },
            label = { Text("Command") },
            modifier = Modifier.fillMaxWidth()
        )
        Spacer(Modifier.height(8.dp))
        Button(
            onClick = { vm.run(listOf(backend), command) },
            enabled = !running && command.isNotBlank() && backend.isNotBlank()
        ) {
            Text(if (running) "Running…" else "Run")
        }
        Spacer(Modifier.height(16.dp))
        LazyColumn {
            items(results) { r ->
                Card(Modifier.fillMaxWidth().padding(vertical = 4.dp)) {
                    Column(Modifier.padding(12.dp)) {
                        Text(r.host, style = MaterialTheme.typography.labelLarge)
                        Text(r.output, style = MaterialTheme.typography.bodySmall)
                    }
                }
            }
        }
    }
}
