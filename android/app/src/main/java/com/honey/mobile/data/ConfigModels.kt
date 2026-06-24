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
    val kubernetes: List<K8sBackendConfig> = emptyList(),
    val aws: List<AwsBackendConfig> = emptyList(),
    val gcp: List<GcpBackendConfig> = emptyList(),
    val consul: List<ConsulBackendConfig> = emptyList(),
    val proxmox: List<ProxmoxBackendConfig> = emptyList(),
    val truenas: List<TrueNasBackendConfig> = emptyList(),
    val docker: List<DockerBackendConfig> = emptyList(),
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

data class K8sBackendConfig(
    val name: String,
    val context: String = "",
    val kubeconfig: String = "",
    val mode: String = "nodes"
)

data class AwsBackendConfig(
    val name: String,
    val profile: String = "",
    val region: String = ""
)

data class GcpBackendConfig(
    val name: String,
    val project: String = "",
    val zone: String = ""
)

data class ConsulBackendConfig(
    val name: String,
    val addr: String = "",
    val token: String = ""
)

data class ProxmoxBackendConfig(
    val name: String,
    val url: String = "",
    val user: String = "",
    val password: String = ""
)

data class TrueNasBackendConfig(
    val name: String,
    val url: String = "",
    val username: String = "",
    @SerializedName("api_key") val apiKey: String = ""
)

data class DockerBackendConfig(
    val name: String,
    val host: String = "",
    val socket: String = ""
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
