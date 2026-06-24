package com.honey.mobile.ui

import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.Delete
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import com.honey.mobile.data.SecretsStore
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import javax.inject.Inject

@HiltViewModel
class SecretsViewModel @Inject constructor(private val store: SecretsStore) : ViewModel() {
    private val _keys = MutableStateFlow<List<String>>(emptyList())
    val keys = _keys.asStateFlow()

    init { refresh() }

    private fun refresh() { _keys.value = store.keys().sorted() }

    fun put(key: String, value: String) { store.put(key.trim(), value); refresh() }
    fun delete(key: String) { store.delete(key); refresh() }
}

@Composable
fun SecretsScreen(vm: SecretsViewModel = hiltViewModel()) {
    val keys by vm.keys.collectAsState()
    var showDialog by remember { mutableStateOf(false) }

    if (showDialog) {
        AddSecretDialog(
            onDismiss = { showDialog = false },
            onSave = { k, v -> vm.put(k, v); showDialog = false }
        )
    }

    Scaffold(
        floatingActionButton = {
            FloatingActionButton(onClick = { showDialog = true }) {
                Icon(Icons.Default.Add, contentDescription = "Add secret")
            }
        }
    ) { padding ->
        if (keys.isEmpty()) {
            Box(Modifier.fillMaxSize().padding(padding), contentAlignment = Alignment.Center) {
                Text("No secrets — tap + to add one")
            }
        } else {
            LazyColumn(Modifier.fillMaxSize().padding(padding)) {
                items(keys) { key ->
                    ListItem(
                        headlineContent = { Text(key) },
                        supportingContent = { Text("••••••") },
                        trailingContent = {
                            IconButton(onClick = { vm.delete(key) }) {
                                Icon(Icons.Default.Delete, contentDescription = "Delete")
                            }
                        }
                    )
                    HorizontalDivider()
                }
            }
        }
    }
}

@Composable
private fun AddSecretDialog(onDismiss: () -> Unit, onSave: (String, String) -> Unit) {
    var key by remember { mutableStateOf("") }
    var value by remember { mutableStateOf("") }
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("Add Secret") },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                OutlinedTextField(
                    value = key, onValueChange = { key = it },
                    label = { Text("Key (e.g. SSH_KEY_PROD)") }, singleLine = true
                )
                OutlinedTextField(
                    value = value, onValueChange = { value = it },
                    label = { Text("Value") }, singleLine = true,
                    visualTransformation = PasswordVisualTransformation()
                )
            }
        },
        confirmButton = {
            TextButton(onClick = { if (key.isNotBlank() && value.isNotBlank()) onSave(key, value) }) {
                Text("Save")
            }
        },
        dismissButton = { TextButton(onClick = onDismiss) { Text("Cancel") } }
    )
}
