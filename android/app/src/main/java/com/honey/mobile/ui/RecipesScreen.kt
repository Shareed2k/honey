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
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch
import mobile.LogCallback
import mobile.Mobile
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

    private val callback = object : LogCallback {
        override fun onLog(msg: String?) {
            // Push to UI state
        }

        override fun onProgress(progressJSON: String?) {
            // Push to UI state
        }
    }

    fun runRecipe(recipe: RecipeEntity) {
        viewModelScope.launch(Dispatchers.IO) {
            try {
                // Call Go code directly!
                val resultJson = Mobile.executeRecipe(recipe.content, callback)
                // Handle result...
            } catch (e: Exception) {
                // Handle error
            }
        }
    }
}

@Composable
fun RecipesScreen(vm: RecipesViewModel = hiltViewModel()) {
    val recipes by vm.recipes.collectAsStateWithLifecycle()
    var showDialog by remember { mutableStateOf(false) }
    var editing by remember { mutableStateOf<RecipeEntity?>(null) }

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
                                IconButton(onClick = { vm.runRecipe(r) }) {
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
