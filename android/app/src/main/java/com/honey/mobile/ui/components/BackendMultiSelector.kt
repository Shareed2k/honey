package com.honey.mobile.ui.components

import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Check
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import com.honey.mobile.api.Backend
import com.honey.mobile.ui.theme.NeonCyan
import com.honey.mobile.ui.theme.NeonViolet

/**
 * Autocomplete multi-select for backends. Shows a filtered dropdown as the user
 * types; selected backends appear as dismissible FilterChips above the field.
 * Empty selection means "all backends".
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun BackendMultiSelector(
    backends: List<Backend>,
    selected: Set<String>,
    onSelectionChange: (Set<String>) -> Unit,
    modifier: Modifier = Modifier,
    label: String = "Backends",
    enabled: Boolean = true,
) {
    var query by remember { mutableStateOf("") }
    var expanded by remember { mutableStateOf(false) }

    val filtered = remember(query, backends) {
        if (query.isBlank()) backends
        else backends.filter { it.name.contains(query, ignoreCase = true) }
    }

    Column(modifier = modifier) {
        if (selected.isNotEmpty()) {
            Row(
                modifier = Modifier
                    .fillMaxWidth()
                    .horizontalScroll(rememberScrollState())
                    .padding(bottom = 6.dp),
                horizontalArrangement = Arrangement.spacedBy(6.dp),
            ) {
                selected.forEach { name ->
                    FilterChip(
                        selected = true,
                        onClick = { if (enabled) onSelectionChange(selected - name) },
                        label = { Text(name, style = MaterialTheme.typography.labelMedium) },
                        colors = FilterChipDefaults.filterChipColors(
                            selectedContainerColor = NeonCyan.copy(alpha = 0.18f),
                            selectedLabelColor = NeonCyan,
                        ),
                    )
                }
            }
        }

        ExposedDropdownMenuBox(
            expanded = expanded,
            onExpandedChange = { if (enabled) expanded = it },
        ) {
            OutlinedTextField(
                value = query,
                onValueChange = { query = it; if (!expanded) expanded = true },
                label = { Text(label) },
                placeholder = {
                    Text(
                        if (selected.isEmpty()) "All backends" else "${selected.size} selected",
                        style = MaterialTheme.typography.bodySmall,
                    )
                },
                trailingIcon = { ExposedDropdownMenuDefaults.TrailingIcon(expanded = expanded) },
                enabled = enabled,
                singleLine = true,
                modifier = Modifier
                    .fillMaxWidth()
                    .menuAnchor(),
            )
            if (filtered.isNotEmpty()) {
                ExposedDropdownMenu(
                    expanded = expanded,
                    onDismissRequest = { expanded = false; query = "" },
                ) {
                    filtered.forEach { backend ->
                        val isSelected = backend.name in selected
                        DropdownMenuItem(
                            text = {
                                Row(
                                    verticalAlignment = Alignment.CenterVertically,
                                    horizontalArrangement = Arrangement.spacedBy(8.dp),
                                ) {
                                    Text(backend.name, modifier = Modifier.weight(1f))
                                    AssistChip(
                                        onClick = {},
                                        label = {
                                            Text(
                                                backend.provider,
                                                style = MaterialTheme.typography.labelSmall,
                                            )
                                        },
                                        colors = AssistChipDefaults.assistChipColors(
                                            labelColor = if (backend.provider == "honey") NeonCyan else NeonViolet,
                                        ),
                                        modifier = Modifier.height(22.dp),
                                    )
                                }
                            },
                            leadingIcon = if (isSelected) {
                                { Icon(Icons.Default.Check, contentDescription = null, tint = NeonCyan, modifier = Modifier.size(18.dp)) }
                            } else null,
                            onClick = {
                                val newSelected = if (isSelected) selected - backend.name else selected + backend.name
                                onSelectionChange(newSelected)
                                query = ""
                                expanded = false
                            },
                        )
                    }
                }
            }
        }
    }
}
