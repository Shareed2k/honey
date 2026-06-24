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
    val loading: Boolean = true
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
                _state.value = DashboardState(
                    online = true,
                    version = meta.version,
                    backendCount = backends.size,
                    loading = false
                )
            } catch (e: Exception) {
                _state.value = DashboardState(online = false, loading = false)
            }
        }
    }
}
