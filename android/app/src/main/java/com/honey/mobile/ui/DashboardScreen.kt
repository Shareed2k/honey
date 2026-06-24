package com.honey.mobile.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
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
        // Header
        Row(verticalAlignment = Alignment.CenterVertically, modifier = Modifier.fillMaxWidth()) {
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
            Spacer(Modifier.weight(1f))
            if (state.loading) {
                CircularProgressIndicator(modifier = Modifier.size(24.dp), strokeWidth = 2.dp)
            }
        }
        
        Spacer(Modifier.height(16.dp))
        
        // Search Form
        OutlinedTextField(
            value = state.nameQuery,
            onValueChange = { vm.updateNameQuery(it) },
            label = { Text("Name") },
            modifier = Modifier.fillMaxWidth(),
            singleLine = true
        )
        
        Spacer(Modifier.height(8.dp))
        
        OutlinedTextField(
            value = state.selectedProviders.joinToString(", "),
            onValueChange = { vm.updateProviders(it) },
            label = { Text("Providers (comma separated)") },
            modifier = Modifier.fillMaxWidth(),
            singleLine = true
        )
        
        Spacer(Modifier.height(8.dp))
        
        OutlinedTextField(
            value = state.selectedBackends.joinToString(", "),
            onValueChange = { vm.updateBackends(it) },
            label = { Text("Backends (comma separated)") },
            modifier = Modifier.fillMaxWidth(),
            singleLine = true
        )
        
        Spacer(Modifier.height(16.dp))
        
        Button(
            onClick = { vm.search() },
            modifier = Modifier.fillMaxWidth(),
            enabled = !state.loading
        ) {
            Text("Search")
        }
        
        Spacer(Modifier.height(16.dp))
        Text("Results (${state.results.size})", style = MaterialTheme.typography.titleMedium)
        Spacer(Modifier.height(8.dp))
        
        LazyColumn(modifier = Modifier.fillMaxSize()) {
            items(state.results) { host ->
                Card(
                    modifier = Modifier.fillMaxWidth().padding(vertical = 4.dp),
                    colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surfaceVariant)
                ) {
                    Column(modifier = Modifier.padding(16.dp)) {
                        Text(host.name, style = MaterialTheme.typography.bodyLarge, color = MaterialTheme.colorScheme.onSurfaceVariant)
                        Text(host.provider, style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant.copy(alpha = 0.7f))
                    }
                }
            }
        }
    }
}
