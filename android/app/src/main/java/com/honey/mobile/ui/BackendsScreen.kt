package com.honey.mobile.ui

import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Dns
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import com.honey.mobile.api.Backend
import com.honey.mobile.api.HoneyApi
import com.honey.mobile.ui.theme.*
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
fun BackendsScreen(
    vm: BackendsViewModel = hiltViewModel(),
) {
    val backends by vm.backends.collectAsStateWithLifecycle()

    GradientBackground {
        if (backends.isEmpty()) {
            Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                Column(horizontalAlignment = Alignment.CenterHorizontally) {
                    Icon(
                        Icons.Outlined.Dns,
                        contentDescription = null,
                        tint = TextDim,
                        modifier = Modifier.size(48.dp),
                    )
                    Spacer(Modifier.height(12.dp))
                    Text("No backends configured", color = TextMid)
                    Text("Go to Config to add one", color = TextDim, style = MaterialTheme.typography.bodySmall)
                }
            }
        } else {
            Column(Modifier.fillMaxSize()) {
                Row(
                    modifier = Modifier
                        .fillMaxWidth()
                        .padding(horizontal = 16.dp, vertical = 14.dp),
                    verticalAlignment = Alignment.CenterVertically,
                    horizontalArrangement = Arrangement.SpaceBetween,
                ) {
                    Text(
                        "Backends",
                        style = MaterialTheme.typography.titleMedium,
                        color = TextHi,
                        fontWeight = FontWeight.SemiBold,
                    )
                    Surface(
                        shape = MaterialTheme.shapes.small,
                        color = NeonCyan.copy(alpha = 0.15f),
                    ) {
                        Text(
                            "${backends.size}",
                            color = NeonCyan,
                            style = MaterialTheme.typography.labelMedium,
                            fontWeight = FontWeight.Bold,
                            modifier = Modifier.padding(horizontal = 10.dp, vertical = 4.dp),
                        )
                    }
                }
                LazyColumn(
                    modifier = Modifier.fillMaxSize(),
                    contentPadding = PaddingValues(horizontal = 16.dp, vertical = 4.dp),
                    verticalArrangement = Arrangement.spacedBy(10.dp),
                ) {
                    items(backends, key = { it.name }) { backend ->
                        BackendCard(backend = backend)
                    }
                    item { Spacer(Modifier.height(8.dp)) }
                }
            }
        }
    }
}

@Composable
private fun BackendCard(backend: Backend) {
    val isHoney = backend.provider == "honey"
    val accentColor = if (isHoney) NeonCyan else NeonViolet

    GlowCard(
        modifier = Modifier.fillMaxWidth(),
        glow = accentColor,
    ) {
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .padding(horizontal = 14.dp, vertical = 12.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Icon(
                Icons.Outlined.Dns,
                contentDescription = null,
                tint = accentColor,
                modifier = Modifier.size(22.dp),
            )
            Spacer(Modifier.width(12.dp))
            Column(modifier = Modifier.weight(1f)) {
                Text(
                    backend.name,
                    style = MaterialTheme.typography.titleSmall,
                    color = TextHi,
                    fontWeight = FontWeight.SemiBold,
                )
                Spacer(Modifier.height(4.dp))
                Row(
                    horizontalArrangement = Arrangement.spacedBy(6.dp),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    AssistChip(
                        onClick = {},
                        label = {
                            Text(
                                backend.provider,
                                style = MaterialTheme.typography.labelSmall,
                            )
                        },
                        colors = AssistChipDefaults.assistChipColors(labelColor = accentColor),
                        modifier = Modifier.height(24.dp),
                    )
                    backend.env?.let { env ->
                        AssistChip(
                            onClick = {},
                            label = { Text(env, style = MaterialTheme.typography.labelSmall) },
                            colors = AssistChipDefaults.assistChipColors(labelColor = TextDim),
                            modifier = Modifier.height(24.dp),
                        )
                    }
                }
            }
        }
    }
}
