package com.honey.mobile.ui

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.honey.mobile.api.HoneyApi
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

data class DashboardState(
    val online: Boolean = false,
    val version: String = "",
    val backendCount: Int = 0,
    val loading: Boolean = true,
    val nameQuery: String = "",
    val selectedProviders: Set<String> = emptySet(),
    val selectedBackends: Set<String> = emptySet(),
    val results: List<com.honey.mobile.api.HostRecord> = emptyList(),
    val availableBackends: List<com.honey.mobile.api.Backend> = emptyList(),
    val error: String? = null
)

@HiltViewModel
class DashboardViewModel @Inject constructor(private val api: HoneyApi) : ViewModel() {
    private val _state = MutableStateFlow(DashboardState())
    val state = _state.asStateFlow()

    init {
        refresh()
    }

    fun refresh() {
        viewModelScope.launch {
            _state.update { it.copy(loading = true, error = null) }
            try {
                val meta = api.getMeta()
                val backends = api.getBackends()
                _state.update { it.copy(
                    online = true,
                    version = meta.version,
                    backendCount = backends.size,
                    availableBackends = backends,
                    loading = false
                ) }
            } catch (e: Exception) {
                _state.update { it.copy(online = false, loading = false, error = e.message ?: "Failed to refresh") }
            }
        }
    }

    fun updateNameQuery(query: String) {
        _state.value = _state.value.copy(nameQuery = query)
    }

    fun toggleProvider(provider: String) {
        val current = _state.value.selectedProviders
        val newSet = if (current.contains(provider)) current - provider else current + provider
        _state.value = _state.value.copy(selectedProviders = newSet)
    }

    fun toggleBackend(backend: String) {
        val current = _state.value.selectedBackends
        val newSet = if (current.contains(backend)) current - backend else current + backend
        _state.value = _state.value.copy(selectedBackends = newSet)
    }

    fun dismissError() {
        _state.value = _state.value.copy(error = null)
    }

    fun search() {
        viewModelScope.launch {
            _state.update { it.copy(loading = true, error = null) }
            try {
                val currentState = _state.value
                val req = com.honey.mobile.api.SearchRequest(
                    name = currentState.nameQuery,
                    providers = currentState.selectedProviders.joinToString(","),
                    backends = currentState.selectedBackends.joinToString(",")
                )
                val results = api.searchHosts(req)
                _state.update { it.copy(results = results, loading = false) }
            } catch (e: Exception) {
                _state.update { it.copy(loading = false, error = e.message ?: "Failed to search") }
            }
        }
    }
}
