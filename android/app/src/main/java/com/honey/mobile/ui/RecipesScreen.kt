package com.honey.mobile.ui

import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import com.honey.mobile.api.HoneyApi
import com.honey.mobile.api.Recipe
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import javax.inject.Inject

@HiltViewModel
class RecipesViewModel @Inject constructor(private val api: HoneyApi) : ViewModel() {
    private val _recipes = MutableStateFlow<List<Recipe>>(emptyList())
    val recipes = _recipes.asStateFlow()

    init {
        viewModelScope.launch {
            runCatching { _recipes.value = api.listRecipes() }
        }
    }
}

@Composable
fun RecipesScreen(vm: RecipesViewModel = hiltViewModel()) {
    val recipes by vm.recipes.collectAsStateWithLifecycle()
    if (recipes.isEmpty()) {
        Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
            Text("No recipes found")
        }
        return
    }
    LazyColumn(Modifier.fillMaxSize()) {
        items(recipes) { r ->
            ListItem(
                headlineContent = { Text(r.name) },
                supportingContent = r.description?.let { { Text(it) } }
            )
            HorizontalDivider()
        }
    }
}
