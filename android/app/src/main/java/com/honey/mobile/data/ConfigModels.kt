package com.honey.mobile.data

import com.google.gson.annotations.SerializedName

data class HoneyConfig(
    val version: Int = 1,
    val defaults: ConfigDefaults = ConfigDefaults(),
    val backends: ConfigBackends = ConfigBackends()
)

data class ConfigDefaults(
    @SerializedName("ssh_user") val sshUser: String = "",
    @SerializedName("cache_ttl") val cacheTtl: String = "",
    @SerializedName("k8s_mode") val k8sMode: String = "nodes"
)

data class ConfigBackends(
    val local: List<LocalBackendConfig> = emptyList(),
    val honey: List<HoneyBackendConfig> = emptyList()
)

data class LocalBackendConfig(
    val name: String,
    val hosts: List<LocalHostConfig> = emptyList()
)

data class LocalHostConfig(
    val name: String,
    @SerializedName("primary_ip") val primaryIp: String,
    @SerializedName("ssh_user") val sshUser: String = ""
)

data class HoneyBackendConfig(
    val name: String,
    val url: String = "",
    val token: String = "",
    val insecure: Boolean = false
)

data class BackendItem(
    val type: String,
    val name: String,
    val subtitle: String
)
