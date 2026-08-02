package com.honey.mobile.vpn

import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import javax.inject.Inject
import javax.inject.Singleton

/**
 * VpnController is the shared in-memory bus between the VPN UI and
 * [HoneyVpnService]. It holds the observable [state] and the pending connect
 * parameters (kept off Intents so PEM/passphrase never leave the process via
 * IPC). The service writes state transitions; the UI reads them.
 */
@Singleton
class VpnController @Inject constructor() {
    private val _state = MutableStateFlow<VpnState>(VpnState.Disconnected)
    val state: StateFlow<VpnState> = _state.asStateFlow()

    /**
     * Connect parameters set by the ViewModel immediately before service start.
     * Carries the in-memory PEM/passphrase so they never leave the process via IPC.
     */
    @Volatile var pendingRequest: VpnRequest? = null

    fun onResolving() { _state.value = VpnState.Resolving }
    fun onConnecting() { _state.value = VpnState.Connecting }

    fun onConnected(exit: String) {
        _state.value = VpnState.Connected(exit)
    }

    fun onStats(stats: VpnStats) {
        _state.update { cur ->
            if (cur is VpnState.Connected) cur.copy(stats = stats) else cur
        }
    }

    fun onError(message: String) { _state.value = VpnState.Error(message) }

    fun onDisconnected() {
        _state.value = VpnState.Disconnected
        pendingRequest = null
    }

    val isActive: Boolean
        get() = state.value.let { it is VpnState.Connected || it is VpnState.Connecting || it is VpnState.Resolving }
}
