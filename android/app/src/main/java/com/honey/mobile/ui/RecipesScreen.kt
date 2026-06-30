package com.honey.mobile.ui

import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.Delete
import androidx.compose.material.icons.filled.Edit
import androidx.compose.material.icons.filled.PlayArrow
import androidx.compose.material.icons.filled.Stop
import androidx.compose.material.icons.outlined.Storage
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import com.honey.mobile.data.RecipeDao
import com.honey.mobile.data.RecipeEntity
import com.honey.mobile.data.SecretsStore
import com.honey.mobile.ui.theme.*
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
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
class RecipesViewModel @Inject constructor(
    private val dao: RecipeDao,
    private val secrets: SecretsStore,
) : ViewModel() {
    val recipes: StateFlow<List<RecipeEntity>> =
        dao.observeAll().stateIn(viewModelScope, SharingStarted.WhileSubscribed(5_000), emptyList())

    private val _runningId = MutableStateFlow<Long?>(null)
    val runningId = _runningId.asStateFlow()

    private val _logLines = MutableStateFlow<List<String>>(emptyList())
    val logLines = _logLines.asStateFlow()

    private val _snackbarEvent = Channel<String>(Channel.BUFFERED)
    val snackbarEvent = _snackbarEvent.receiveAsFlow()

    private var runJob: Job? = null

    fun save(name: String, desc: String, content: String, id: Long = 0) {
        viewModelScope.launch {
            dao.upsert(RecipeEntity(id = id, name = name, description = desc, content = content))
        }
    }

    fun delete(recipe: RecipeEntity) {
        viewModelScope.launch { dao.delete(recipe) }
    }

    fun stopRecipe() {
        runJob?.cancel()
        _runningId.value = null
        _logLines.value = emptyList()
    }

    fun runRecipe(recipe: RecipeEntity) {
        if (_runningId.value != null) return
        runJob = viewModelScope.launch(Dispatchers.IO) {
            _runningId.value = recipe.id
            _logLines.value = emptyList()
            val callback = object : LogCallback {
                override fun onLog(msg: String?) {
                    if (msg != null) {
                        _logLines.update { (it + msg).takeLast(50) }
                    }
                }
                override fun onProgress(progressJSON: String?) {}
            }
            try {
                val resolvedContent = secrets.resolve(recipe.content)
                val jsonRequest = JSONObject().put("recipe", resolvedContent).toString()
                val resultJson = Mobile.executeRecipe(jsonRequest, callback)
                _snackbarEvent.send("Done: ${resultJson ?: "success"}")
            } catch (e: Exception) {
                if (e !is kotlinx.coroutines.CancellationException) {
                    _snackbarEvent.send("Error: ${e.message ?: "unknown error"}")
                }
            } finally {
                _runningId.value = null
            }
        }
    }
}

@Composable
fun RecipesScreen(vm: RecipesViewModel = hiltViewModel()) {
    val recipes by vm.recipes.collectAsStateWithLifecycle()
    val runningId by vm.runningId.collectAsStateWithLifecycle()
    val logLines by vm.logLines.collectAsStateWithLifecycle()
    var showDialog by remember { mutableStateOf(false) }
    var editing by remember { mutableStateOf<RecipeEntity?>(null) }
    val snackbarHostState = remember { SnackbarHostState() }

    LaunchedEffect(Unit) {
        vm.snackbarEvent.collect { msg -> snackbarHostState.showSnackbar(msg) }
    }

    if (showDialog) {
        RecipeDialog(
            initial = editing,
            onDismiss = { showDialog = false; editing = null },
            onSave = { name, desc, content ->
                vm.save(name, desc, content, editing?.id ?: 0)
                showDialog = false; editing = null
            },
        )
    }

    GradientBackground {
        Scaffold(
            containerColor = Color.Transparent,
            snackbarHost = { SnackbarHost(snackbarHostState) },
            floatingActionButton = {
                FloatingActionButton(
                    onClick = { editing = null; showDialog = true },
                    containerColor = NeonCyan,
                    contentColor = OnNeon,
                ) {
                    Icon(Icons.Default.Add, contentDescription = "New recipe")
                }
            },
        ) { padding ->
            if (recipes.isEmpty()) {
                Box(
                    Modifier.fillMaxSize().padding(padding),
                    contentAlignment = Alignment.Center,
                ) {
                    Column(horizontalAlignment = Alignment.CenterHorizontally) {
                        Icon(
                            Icons.Outlined.Storage,
                            contentDescription = null,
                            tint = TextDim,
                            modifier = Modifier.size(52.dp),
                        )
                        Spacer(Modifier.height(12.dp))
                        Text("No recipes yet", color = TextMid, fontWeight = FontWeight.SemiBold)
                        Text("Tap + to create one", color = TextDim, style = MaterialTheme.typography.bodySmall)
                    }
                }
            } else {
                LazyColumn(
                    modifier = Modifier.fillMaxSize().padding(padding),
                    contentPadding = PaddingValues(horizontal = 16.dp, vertical = 12.dp),
                    verticalArrangement = Arrangement.spacedBy(10.dp),
                ) {
                    items(recipes, key = { it.id }) { r ->
                        val isRunning = runningId == r.id
                        RecipeCard(
                            recipe = r,
                            isRunning = isRunning,
                            logLines = if (isRunning) logLines else emptyList(),
                            onRun = { vm.runRecipe(r) },
                            onStop = { vm.stopRecipe() },
                            onEdit = { editing = r; showDialog = true },
                            onDelete = { vm.delete(r) },
                            anyRunning = runningId != null,
                        )
                    }
                    item { Spacer(Modifier.height(72.dp)) }
                }
            }
        }
    }
}

@Composable
private fun RecipeCard(
    recipe: RecipeEntity,
    isRunning: Boolean,
    logLines: List<String>,
    anyRunning: Boolean,
    onRun: () -> Unit,
    onStop: () -> Unit,
    onEdit: () -> Unit,
    onDelete: () -> Unit,
) {
    GlowCard(
        modifier = Modifier.fillMaxWidth(),
        glow = if (isRunning) NeonCyan else NeonViolet,
    ) {
        Column(Modifier.padding(14.dp)) {
            Text(
                recipe.name,
                style = MaterialTheme.typography.titleSmall,
                color = NeonCyan,
                fontWeight = FontWeight.SemiBold,
            )
            if (recipe.description.isNotBlank()) {
                Spacer(Modifier.height(2.dp))
                Text(
                    recipe.description,
                    style = MaterialTheme.typography.bodySmall,
                    color = TextMid,
                )
            }

            if (isRunning) {
                Spacer(Modifier.height(10.dp))
                LinearProgressIndicator(
                    modifier = Modifier.fillMaxWidth(),
                    color = NeonCyan,
                    trackColor = BgPanel,
                )
                if (logLines.isNotEmpty()) {
                    Spacer(Modifier.height(8.dp))
                    val listState = rememberLazyListState()
                    LaunchedEffect(logLines.size) {
                        if (logLines.isNotEmpty()) listState.animateScrollToItem(logLines.lastIndex)
                    }
                    LazyColumn(
                        state = listState,
                        modifier = Modifier
                            .fillMaxWidth()
                            .heightIn(max = 120.dp),
                    ) {
                        items(logLines) { line ->
                            Text(
                                line,
                                style = MonoStyle.copy(fontSize = MaterialTheme.typography.bodySmall.fontSize),
                                color = TextMid,
                            )
                        }
                    }
                }
                Spacer(Modifier.height(8.dp))
                Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.End) {
                    IconButton(onClick = onStop) {
                        Icon(Icons.Default.Stop, contentDescription = "Stop", tint = NeonRed, modifier = Modifier.size(20.dp))
                    }
                }
            } else {
                Spacer(Modifier.height(8.dp))
                Row(
                    Modifier.fillMaxWidth(),
                    horizontalArrangement = Arrangement.End,
                ) {
                    IconButton(onClick = onRun, enabled = !anyRunning) {
                        Icon(Icons.Default.PlayArrow, contentDescription = "Run", tint = if (!anyRunning) NeonGreen else TextDim, modifier = Modifier.size(20.dp))
                    }
                    IconButton(onClick = onEdit) {
                        Icon(Icons.Default.Edit, contentDescription = "Edit", tint = NeonCyan, modifier = Modifier.size(20.dp))
                    }
                    IconButton(onClick = onDelete) {
                        Icon(Icons.Default.Delete, contentDescription = "Delete", tint = NeonRed, modifier = Modifier.size(20.dp))
                    }
                }
            }
        }
    }
}

@Composable
private fun RecipeDialog(
    initial: RecipeEntity?,
    onDismiss: () -> Unit,
    onSave: (name: String, desc: String, content: String) -> Unit,
) {
    var name by remember { mutableStateOf(initial?.name ?: "") }
    var desc by remember { mutableStateOf(initial?.description ?: "") }
    var content by remember { mutableStateOf(initial?.content ?: "") }

    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(if (initial == null) "New Recipe" else "Edit Recipe") },
        text = {
            Column(
                modifier = Modifier.verticalScroll(rememberScrollState()),
                verticalArrangement = Arrangement.spacedBy(8.dp),
            ) {
                OutlinedTextField(
                    value = name,
                    onValueChange = { name = it },
                    label = { Text("Name") },
                    singleLine = true,
                    modifier = Modifier.fillMaxWidth(),
                )
                OutlinedTextField(
                    value = desc,
                    onValueChange = { desc = it },
                    label = { Text("Description") },
                    singleLine = true,
                    modifier = Modifier.fillMaxWidth(),
                )
                OutlinedTextField(
                    value = content,
                    onValueChange = { content = it },
                    label = { Text("CUE content") },
                    minLines = 5,
                    maxLines = 10,
                    modifier = Modifier.fillMaxWidth(),
                )
            }
        },
        confirmButton = {
            TextButton(onClick = { if (name.isNotBlank()) onSave(name, desc, content) }) {
                Text("Save", color = NeonCyan)
            }
        },
        dismissButton = {
            TextButton(onClick = onDismiss) { Text("Cancel") }
        },
    )
}
