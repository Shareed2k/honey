package com.honey.mobile.ui

import androidx.compose.foundation.clickable
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
import com.honey.mobile.api.Backend
import com.honey.mobile.api.HoneyApi
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import javax.inject.Inject

@HiltViewModel
class BackendsViewModel @Inject constructor(private val api: HoneyApi) : ViewModel() {
    private val _backends = MutableStateFlow<List<Backend>>(emptyList())
    val backends = _backends.asStateFlow()

    init {
        viewModelScope.launch {
            runCatching { _backends.value = api.getBackends() }
        }
    }
}

@Composable
fun BackendsScreen(onSelectBackend: (String) -> Unit = {}, vm: BackendsViewModel = hiltViewModel()) {
    val backends by vm.backends.collectAsStateWithLifecycle()
    LazyColumn(Modifier.fillMaxSize()) {
        items(backends) { b ->
            ListItem(
                headlineContent = { Text(b.name) },
                supportingContent = { Text("${b.provider}${b.env?.let { " · $it" } ?: ""}") },
                modifier = Modifier.clickable { onSelectBackend(b.name) }
            )
            HorizontalDivider()
        }
    }
}
