package com.honey.mobile.ui

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.honey.mobile.api.HoneyApi
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import javax.inject.Inject

data class DashboardState(
    val online: Boolean = false,
    val version: String = "",
    val backendCount: Int = 0,
    val loading: Boolean = true,
    val nameQuery: String = "",
    val selectedProviders: List<String> = emptyList(),
    val selectedBackends: List<String> = emptyList(),
    val results: List<com.honey.mobile.api.HostRecord> = emptyList()
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
            _state.value = _state.value.copy(loading = true)
            try {
                val meta = api.getMeta()
                val backends = api.getBackends()
                _state.value = _state.value.copy(
                    online = true,
                    version = meta.version,
                    backendCount = backends.size,
                    loading = false
                )
            } catch (e: Exception) {
                _state.value = _state.value.copy(online = false, loading = false)
            }
        }
    }

    fun updateNameQuery(query: String) {
        _state.value = _state.value.copy(nameQuery = query)
    }

    fun updateProviders(providers: String) {
        val list = providers.split(",").map { it.trim() }.filter { it.isNotEmpty() }
        _state.value = _state.value.copy(selectedProviders = list)
    }

    fun updateBackends(backends: String) {
        val list = backends.split(",").map { it.trim() }.filter { it.isNotEmpty() }
        _state.value = _state.value.copy(selectedBackends = list)
    }

    fun search() {
        viewModelScope.launch {
            _state.value = _state.value.copy(loading = true)
            try {
                val state = _state.value
                val req = com.honey.mobile.api.SearchRequest(
                    name = state.nameQuery,
                    providers = state.selectedProviders.joinToString(","),
                    backends = state.selectedBackends.joinToString(",")
                )
                val results = api.searchHosts(req)
                _state.value = state.copy(results = results, loading = false)
            } catch (e: Exception) {
                _state.value = _state.value.copy(loading = false)
            }
        }
    }
}
