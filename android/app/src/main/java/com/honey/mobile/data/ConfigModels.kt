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
    @SerializedName("local") val local: List<LocalBackendConfig> = emptyList(),
    @SerializedName("honey") val honey: List<HoneyBackendConfig> = emptyList()
)

data class LocalBackendConfig(
    @SerializedName("name") val name: String,
    @SerializedName("hosts") val hosts: List<LocalHostConfig> = emptyList()
)

data class LocalHostConfig(
    @SerializedName("name") val name: String,
    @SerializedName("primary_ip") val primaryIp: String,
    @SerializedName("ssh_user") val sshUser: String = ""
)

data class HoneyBackendConfig(
    @SerializedName("name") val name: String,
    @SerializedName("url") val url: String = "",
    @SerializedName("token") val token: String = "",
    @SerializedName("insecure") val insecure: Boolean = false
)

data class BackendItem(
    val type: String,
    val name: String,
    val subtitle: String
)
