package com.honey.mobile.ui

import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.Delete
import androidx.compose.material.icons.filled.Edit
import androidx.compose.material.icons.filled.PlayArrow
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import com.honey.mobile.data.RecipeDao
import com.honey.mobile.data.RecipeEntity
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.receiveAsFlow
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import mobile.LogCallback
import mobile.Mobile
import org.json.JSONObject
import javax.inject.Inject

@HiltViewModel
class RecipesViewModel @Inject constructor(private val dao: RecipeDao) : ViewModel() {
    val recipes: StateFlow<List<RecipeEntity>> =
        dao.observeAll().stateIn(viewModelScope, SharingStarted.WhileSubscribed(5_000), emptyList())

    fun save(name: String, desc: String, content: String, id: Long = 0) {
        viewModelScope.launch {
            dao.upsert(RecipeEntity(id = id, name = name, description = desc, content = content))
        }
    }

    fun delete(recipe: RecipeEntity) {
        viewModelScope.launch { dao.delete(recipe) }
    }

    sealed class ExecutionState {
        object Idle : ExecutionState()
        data class Running(val progress: String? = null, val log: String? = null) : ExecutionState()
    }

    private val _executionState = MutableStateFlow<ExecutionState>(ExecutionState.Idle)
    val executionState: StateFlow<ExecutionState> = _executionState.asStateFlow()

    private val _snackbarEvent = Channel<String>(Channel.BUFFERED)
    val snackbarEvent = _snackbarEvent.receiveAsFlow()

    fun runRecipe(recipe: RecipeEntity) {
        if (_executionState.value is ExecutionState.Running) return

        viewModelScope.launch(Dispatchers.IO) {
            _executionState.value = ExecutionState.Running()
            
            val callback = object : LogCallback {
                override fun onLog(msg: String?) {
                    android.util.Log.d("RecipesViewModel", "onLog: $msg")
                    _executionState.update { state ->
                        (state as? ExecutionState.Running)?.copy(log = msg) ?: ExecutionState.Running(log = msg)
                    }
                }

                override fun onProgress(progressJSON: String?) {
                    android.util.Log.d("RecipesViewModel", "onProgress: $progressJSON")
                    _executionState.update { state ->
                        (state as? ExecutionState.Running)?.copy(progress = progressJSON) ?: ExecutionState.Running(progress = progressJSON)
                    }
                }
            }

            try {
                // Call Go code directly! Data boundary must use JSON strings.
                val jsonRequest = JSONObject().put("recipe", recipe.content).toString()
                val resultJson = Mobile.executeRecipe(jsonRequest, callback)
                _executionState.value = ExecutionState.Idle
                _snackbarEvent.send("Success: ${resultJson ?: "Success"}")
            } catch (e: Exception) {
                android.util.Log.e("RecipesViewModel", "Error running recipe", e)
                _executionState.value = ExecutionState.Idle
                _snackbarEvent.send("Error: ${e.message ?: "Unknown error"}")
            }
        }
    }
}

@Composable
fun RecipesScreen(vm: RecipesViewModel = hiltViewModel()) {
    val recipes by vm.recipes.collectAsStateWithLifecycle()
    val executionState by vm.executionState.collectAsStateWithLifecycle()
    var showDialog by remember { mutableStateOf(false) }
    var editing by remember { mutableStateOf<RecipeEntity?>(null) }
    val snackbarHostState = remember { SnackbarHostState() }

    LaunchedEffect(Unit) {
        vm.snackbarEvent.collect { msg ->
            snackbarHostState.showSnackbar(msg)
        }
    }

    if (showDialog) {
        RecipeDialog(
            initial = editing,
            onDismiss = { showDialog = false; editing = null },
            onSave = { name, desc, content ->
                vm.save(name, desc, content, editing?.id ?: 0)
                showDialog = false; editing = null
            }
        )
    }

    Scaffold(
        snackbarHost = { SnackbarHost(snackbarHostState) },
        floatingActionButton = {
            FloatingActionButton(onClick = { editing = null; showDialog = true }) {
                Icon(Icons.Default.Add, contentDescription = "New recipe")
            }
        }
    ) { padding ->
        if (recipes.isEmpty()) {
            Box(Modifier.fillMaxSize().padding(padding), contentAlignment = Alignment.Center) {
                Text("No recipes — tap + to create one")
            }
        } else {
            LazyColumn(Modifier.fillMaxSize().padding(padding)) {
                items(recipes, key = { it.id }) { r ->
                    ListItem(
                        headlineContent = { Text(r.name) },
                        supportingContent = { if (r.description.isNotBlank()) Text(r.description) },
                        trailingContent = {
                            Row {
                                val isRunning = executionState is RecipesViewModel.ExecutionState.Running
                                IconButton(
                                    onClick = { vm.runRecipe(r) },
                                    enabled = !isRunning
                                ) {
                                    Icon(Icons.Default.PlayArrow, contentDescription = "Run")
                                }
                                IconButton(onClick = { editing = r; showDialog = true }) {
                                    Icon(Icons.Default.Edit, contentDescription = "Edit")
                                }
                                IconButton(onClick = { vm.delete(r) }) {
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
}

@Composable
private fun RecipeDialog(
    initial: RecipeEntity?,
    onDismiss: () -> Unit,
    onSave: (name: String, desc: String, content: String) -> Unit
) {
    var name by remember { mutableStateOf(initial?.name ?: "") }
    var desc by remember { mutableStateOf(initial?.description ?: "") }
    var content by remember { mutableStateOf(initial?.content ?: "") }

    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(if (initial == null) "New Recipe" else "Edit Recipe") },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                OutlinedTextField(value = name, onValueChange = { name = it }, label = { Text("Name") }, singleLine = true)
                OutlinedTextField(value = desc, onValueChange = { desc = it }, label = { Text("Description") }, singleLine = true)
                OutlinedTextField(
                    value = content,
                    onValueChange = { content = it },
                    label = { Text("CUE content") },
                    minLines = 4,
                    maxLines = 8
                )
            }
        },
        confirmButton = {
            TextButton(onClick = { if (name.isNotBlank()) onSave(name, desc, content) }) {
                Text("Save")
            }
        },
        dismissButton = { TextButton(onClick = onDismiss) { Text("Cancel") } }
    )
}
