package com.honey.mobile.api

data class MetaResponse(
    val version: String,
    val features: Map<String, Boolean> = emptyMap()
)

data class Backend(
    val name: String,
    val provider: String,
    val env: String? = null
)

data class SearchRequest(
    val name: String = "",
    val providers: String = "",
    val backends: String = ""
)

data class SearchResponse(
    val records: List<HostRecord>,
    val count: Int
)

data class HostRecord(
    val name: String,
    val provider: String,
    val groups: List<String> = emptyList(),
    val backendName: String = "",
    val primaryIp: String = "",
    val sshPort: Int = 0,
    // Non-empty when this record was resolved through a configured
    // `backends.honey` entry (Go side: internal/provider/honeyprovider's
    // Search tags records with Meta["honey_upstream_backend"]). When set,
    // exec/VPN for this host runs on the provider side over mTLS/mesh — the
    // SSH private-key picker is not needed and should be hidden.
    val honeyUpstreamBackend: String = "",
)

data class ExecRequest(
    val backends: List<String>,
    val command: String,
    val name: String = "",
    val nameRegex: String = "",
    val providers: String = "",
    val hostIp: String = "",
    val sshPort: Int = 0,
    val sshUser: String = "",
    val dry_run: Boolean = false,
    val sshIdentityFile: String = "",
    val sshIdentityPassphrase: String = ""
)

data class ExecResult(
    val host: String,
    val output: String,
    val exit_code: Int,
    val error: String? = null
)

data class Recipe(
    val name: String,
    val description: String? = null
)

data class CueExecRequest(
    val content: String,
    val backends: List<String>,
    val execute: Boolean = false
)

data class StepRisk(
    val step_index: Int,
    val kind: String,
    val host: String,
    val command: String
)

data class CueExecResult(
    val plan: String,
    val risk_assessment: List<StepRisk>? = null
)
