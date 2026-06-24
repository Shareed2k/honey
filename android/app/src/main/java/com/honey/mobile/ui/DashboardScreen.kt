package com.honey.mobile.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle

@Composable
fun DashboardScreen(vm: DashboardViewModel = hiltViewModel()) {
    val state by vm.state.collectAsStateWithLifecycle()
    Column(Modifier.fillMaxSize().padding(16.dp)) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Box(
                Modifier.size(12.dp).background(
                    if (state.online) Color(0xFF4CAF50) else Color(0xFFF44336), CircleShape
                )
            )
            Spacer(Modifier.width(8.dp))
            Text(
                if (state.online) "Honey ${state.version}" else "Honey — offline",
                style = MaterialTheme.typography.titleMedium
            )
        }
        Spacer(Modifier.height(8.dp))
        Text("Backends: ${state.backendCount}", style = MaterialTheme.typography.bodyMedium)
        if (state.loading) {
            Spacer(Modifier.height(16.dp))
            CircularProgressIndicator()
        }
        Spacer(Modifier.height(16.dp))
        Button(onClick = { vm.refresh() }) { Text("Refresh") }
    }
}
