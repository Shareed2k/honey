package com.honey.mobile.vpn

/** Live throughput sample reported by the Go engine. */
data class VpnStats(
    val upTotal: Long = 0,
    val downTotal: Long = 0,
    val upRate: Long = 0,
    val downRate: Long = 0,
    val uptimeSeconds: Long = 0,
)

/** UI-facing VPN lifecycle state. */
sealed interface VpnState {
    data object Disconnected : VpnState
    data object Resolving : VpnState
    data object Connecting : VpnState
    data class Connected(val exit: String, val stats: VpnStats = VpnStats()) : VpnState
    data class Error(val message: String) : VpnState
}

/**
 * Parameters chosen on the VPN screen, passed to the service as a request.
 * The SSH key is decrypted in the Activity (behind a biometric prompt) and
 * carried here in-memory — the service never reads the keystore.
 */
data class VpnRequest(
    val exitName: String,
    val hostIp: String,
    val sshPort: Int,
    val sshUser: String,
    val sshKeyPem: String = "",
    val sshKeyPassphrase: String = "",
    val backends: String = "",
    val providers: String = "",
    val nameRegex: String = "",
)
