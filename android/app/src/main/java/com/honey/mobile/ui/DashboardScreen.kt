package com.honey.mobile.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.ContentCopy
import androidx.compose.material.icons.filled.PlayArrow
import androidx.compose.material.icons.outlined.Search
import androidx.compose.material.icons.outlined.Shield
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalClipboardManager
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.compose.ui.platform.LocalLifecycleOwner
import com.honey.mobile.api.HostRecord
import com.honey.mobile.ui.components.BackendMultiSelector
import com.honey.mobile.ui.theme.*

@Composable
fun DashboardScreen(
    onNavigateExec: (hostName: String, provider: String, ip: String, sshPort: Int) -> Unit = { _, _, _, _ -> },
    onNavigateVpn: (hostName: String, ip: String, sshPort: Int) -> Unit = { _, _, _ -> },
    vm: DashboardViewModel = hiltViewModel(),
) {
    val state by vm.state.collectAsStateWithLifecycle()
    val lifecycleOwner = LocalLifecycleOwner.current
    val clipboard = LocalClipboardManager.current

    DisposableEffect(lifecycleOwner) {
        val observer = LifecycleEventObserver { _, event ->
            if (event == Lifecycle.Event.ON_RESUME) vm.refresh()
        }
        lifecycleOwner.lifecycle.addObserver(observer)
        onDispose { lifecycleOwner.lifecycle.removeObserver(observer) }
    }

    val uniqueProviders = remember(state.availableBackends) {
        state.availableBackends.map { it.provider }.distinct().sorted()
    }

    GradientBackground {
        LazyColumn(
            modifier = Modifier.fillMaxSize(),
            contentPadding = PaddingValues(16.dp),
            verticalArrangement = Arrangement.spacedBy(12.dp),
        ) {
            // Status header
            item {
                GlowCard(
                    glow = if (state.online) NeonGreen else NeonRed,
                    modifier = Modifier.fillMaxWidth(),
                ) {
                    Row(
                        modifier = Modifier
                            .fillMaxWidth()
                            .padding(horizontal = 14.dp, vertical = 12.dp),
                        verticalAlignment = Alignment.CenterVertically,
                    ) {
                        Box(
                            Modifier
                                .size(10.dp)
                                .background(
                                    if (state.online) NeonGreen else NeonRed,
                                    CircleShape,
                                ),
                        )
                        Spacer(Modifier.width(10.dp))
                        Text(
                            if (state.online) "Honey ${state.version}" else "Honey — offline",
                            style = MaterialTheme.typography.titleSmall,
                            color = if (state.online) NeonGreen else NeonRed,
                            fontWeight = FontWeight.SemiBold,
                        )
                        if (state.loading) {
                            Spacer(Modifier.weight(1f))
                            CircularProgressIndicator(
                                modifier = Modifier.size(18.dp),
                                strokeWidth = 2.dp,
                                color = NeonCyan,
                            )
                        }
                    }
                }
            }

            // Search form
            item {
                GlowCard(modifier = Modifier.fillMaxWidth()) {
                    Column(
                        modifier = Modifier.padding(14.dp),
                        verticalArrangement = Arrangement.spacedBy(10.dp),
                    ) {
                        OutlinedTextField(
                            value = state.nameQuery,
                            onValueChange = { vm.updateNameQuery(it) },
                            label = { Text("Name") },
                            leadingIcon = {
                                Icon(
                                    Icons.Outlined.Search,
                                    contentDescription = null,
                                    tint = TextDim,
                                    modifier = Modifier.size(18.dp),
                                )
                            },
                            modifier = Modifier.fillMaxWidth(),
                            singleLine = true,
                        )

                        // Provider filter chips
                        if (uniqueProviders.isNotEmpty()) {
                            Text(
                                "Providers",
                                style = MaterialTheme.typography.labelSmall,
                                color = TextDim,
                            )
                            LazyRow(horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                                items(uniqueProviders) { provider ->
                                    FilterChip(
                                        selected = provider in state.selectedProviders,
                                        onClick = { vm.toggleProvider(provider) },
                                        label = { Text(provider) },
                                        colors = FilterChipDefaults.filterChipColors(
                                            selectedContainerColor = NeonViolet.copy(alpha = 0.18f),
                                            selectedLabelColor = NeonViolet,
                                        ),
                                    )
                                }
                            }
                        }

                        BackendMultiSelector(
                            backends = state.availableBackends,
                            selected = state.selectedBackends,
                            onSelectionChange = { newSet ->
                                (newSet - state.selectedBackends).forEach { vm.toggleBackend(it) }
                                (state.selectedBackends - newSet).forEach { vm.toggleBackend(it) }
                            },
                            modifier = Modifier.fillMaxWidth(),
                        )

                        NeonButton(
                            onClick = { vm.search() },
                            enabled = !state.loading,
                            modifier = Modifier.fillMaxWidth(),
                        ) {
                            Icon(
                                Icons.Outlined.Search,
                                contentDescription = null,
                                modifier = Modifier.size(18.dp),
                            )
                            Spacer(Modifier.width(8.dp))
                            Text("Search", fontWeight = FontWeight.Bold)
                        }
                    }
                }
            }

            // Error
            if (state.error != null) {
                item {
                    GlowCard(glow = NeonRed, modifier = Modifier.fillMaxWidth()) {
                        Text(
                            state.error!!,
                            color = NeonRed,
                            style = MaterialTheme.typography.bodyMedium,
                            modifier = Modifier.padding(14.dp),
                        )
                    }
                }
            }

            // Results header
            if (state.results.isNotEmpty() || state.loading) {
                item {
                    Row(
                        modifier = Modifier.fillMaxWidth().padding(horizontal = 2.dp),
                        verticalAlignment = Alignment.CenterVertically,
                        horizontalArrangement = Arrangement.SpaceBetween,
                    ) {
                        Text(
                            "Results",
                            style = MaterialTheme.typography.titleSmall,
                            color = TextMid,
                            fontWeight = FontWeight.SemiBold,
                        )
                        if (state.results.isNotEmpty()) {
                            Surface(
                                shape = MaterialTheme.shapes.small,
                                color = NeonCyan.copy(alpha = 0.12f),
                            ) {
                                Text(
                                    "${state.results.size}",
                                    color = NeonCyan,
                                    style = MaterialTheme.typography.labelSmall,
                                    fontWeight = FontWeight.Bold,
                                    modifier = Modifier.padding(horizontal = 8.dp, vertical = 3.dp),
                                )
                            }
                        }
                    }
                }
            }

            // Loading state
            if (state.loading && state.results.isEmpty()) {
                item {
                    Box(
                        Modifier
                            .fillMaxWidth()
                            .height(160.dp),
                        contentAlignment = Alignment.Center,
                    ) {
                        StatusRing(state = RingState.Connecting, diameter = 72.dp) {}
                    }
                }
            }

            // Host result cards
            items(state.results) { host ->
                HostCard(
                    host = host,
                    onCopy = { clipboard.setText(AnnotatedString(host.name)) },
                    onExec = { onNavigateExec(host.name, host.provider, host.primaryIp, host.sshPort) },
                    onVpn = { onNavigateVpn(host.name, host.primaryIp, host.sshPort) },
                )
            }

            item { Spacer(Modifier.height(8.dp)) }
        }
    }
}

@Composable
private fun HostCard(
    host: HostRecord,
    onCopy: () -> Unit,
    onExec: () -> Unit,
    onVpn: () -> Unit,
) {
    val isHoney = host.provider == "honey"
    val providerColor = if (isHoney) NeonCyan else NeonViolet

    GlowCard(modifier = Modifier.fillMaxWidth(), glow = NeonCyan) {
        Column(modifier = Modifier.padding(14.dp)) {
            Row(
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.SpaceBetween,
                modifier = Modifier.fillMaxWidth(),
            ) {
                Text(
                    host.name,
                    style = MaterialTheme.typography.titleSmall,
                    color = NeonCyan,
                    fontWeight = FontWeight.SemiBold,
                    modifier = Modifier.weight(1f),
                )
                Row {
                    IconButton(onClick = onCopy, modifier = Modifier.size(32.dp)) {
                        Icon(
                            Icons.Default.ContentCopy,
                            contentDescription = "Copy hostname",
                            tint = TextDim,
                            modifier = Modifier.size(16.dp),
                        )
                    }
                    IconButton(onClick = onExec, modifier = Modifier.size(32.dp)) {
                        Icon(
                            Icons.Default.PlayArrow,
                            contentDescription = "Exec on host",
                            tint = NeonGreen,
                            modifier = Modifier.size(18.dp),
                        )
                    }
                    IconButton(onClick = onVpn, modifier = Modifier.size(32.dp)) {
                        Icon(
                            Icons.Outlined.Shield,
                            contentDescription = "Use as VPN exit",
                            tint = NeonViolet,
                            modifier = Modifier.size(18.dp),
                        )
                    }
                }
            }
            Spacer(Modifier.height(6.dp))
            Row(
                horizontalArrangement = Arrangement.spacedBy(6.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                AssistChip(
                    onClick = {},
                    label = { Text(host.provider, style = MaterialTheme.typography.labelSmall) },
                    colors = AssistChipDefaults.assistChipColors(labelColor = providerColor),
                    modifier = Modifier.height(22.dp),
                )
                host.groups.take(3).forEach { group ->
                    AssistChip(
                        onClick = {},
                        label = { Text(group, style = MaterialTheme.typography.labelSmall) },
                        colors = AssistChipDefaults.assistChipColors(labelColor = TextDim),
                        modifier = Modifier.height(22.dp),
                    )
                }
            }
        }
    }
}
