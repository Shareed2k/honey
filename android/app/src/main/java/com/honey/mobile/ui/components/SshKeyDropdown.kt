package com.honey.mobile.ui.components

import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.ArrowDropDown
import androidx.compose.material3.*
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import com.honey.mobile.data.SshKeyMeta

/** Reusable SSH-key selector. null selection = "Default / agent". */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SshKeyDropdown(
    keys: List<SshKeyMeta>,
    selectedId: String?,
    onSelect: (String?) -> Unit,
    modifier: Modifier = Modifier,
    enabled: Boolean = true,
) {
    var expanded by remember { mutableStateOf(false) }
    val selectedLabel = keys.firstOrNull { it.id == selectedId }?.let { "${it.name} (${it.type})" }
        ?: "Default / agent"

    ExposedDropdownMenuBox(
        expanded = expanded,
        onExpandedChange = { if (enabled) expanded = it },
        modifier = modifier,
    ) {
        OutlinedTextField(
            value = selectedLabel,
            onValueChange = {},
            readOnly = true,
            enabled = enabled,
            label = { Text("SSH key") },
            trailingIcon = { Icon(Icons.Outlined.ArrowDropDown, contentDescription = null) },
            modifier = Modifier.fillMaxWidth().menuAnchor(),
        )
        ExposedDropdownMenu(expanded = expanded, onDismissRequest = { expanded = false }) {
            DropdownMenuItem(
                text = { Text("Default / agent") },
                onClick = { onSelect(null); expanded = false },
            )
            keys.forEach { key ->
                DropdownMenuItem(
                    text = { Text("${key.name} (${key.type})") },
                    onClick = { onSelect(key.id); expanded = false },
                )
            }
        }
    }
}
