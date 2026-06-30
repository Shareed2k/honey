package com.honey.mobile.api

import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.components.SingletonComponent
import dagger.hilt.android.qualifiers.ApplicationContext
import android.content.Context
import javax.inject.Singleton
import org.json.JSONObject
import org.json.JSONArray
import mobile.Mobile
import kotlinx.coroutines.withContext
import kotlinx.coroutines.Dispatchers

@Module
@InstallIn(SingletonComponent::class)
object ApiModule {
    @Provides
    @Singleton
    fun provideHoneyApi(@ApplicationContext context: Context): HoneyApi {
        val configPath = "${context.filesDir.absolutePath}/config/config.yaml"
        return object : HoneyApi {
            override suspend fun getMeta(): MetaResponse = withContext(Dispatchers.IO) {
                MetaResponse(version = Mobile.getVersion())
            }

            override suspend fun getBackends(): List<Backend> = withContext(Dispatchers.IO) {
                val reqJson = JSONObject().apply {
                    put("config_path", configPath)
                }.toString()

                val respJson = Mobile.listBackends(reqJson)
                val jsonObj = JSONObject(respJson)
                val backendsArray = jsonObj.optJSONArray("backends") ?: JSONArray()

                val parsedBackends = mutableListOf<Backend>()
                for (i in 0 until backendsArray.length()) {
                    val backendObj = backendsArray.getJSONObject(i)
                    val name = backendObj.optString("name", "")
                    val provider = backendObj.optString("kind", "")
                    val env = if (backendObj.has("env")) backendObj.getString("env") else null
                    parsedBackends.add(Backend(name = name, provider = provider, env = env))
                }
                parsedBackends
            }

            override suspend fun searchHosts(req: SearchRequest): List<HostRecord> = withContext(Dispatchers.IO) {
                val reqJson = JSONObject().apply {
                    put("config_path", configPath)
                    put("name", req.name)
                    put("providers", req.providers)
                    put("backends", req.backends)
                }.toString()

                val respJson = Mobile.searchHosts(reqJson)
                val jsonObj = JSONObject(respJson)
                val recordsArray = jsonObj.optJSONArray("records") ?: JSONArray()

                val parsedRecords = mutableListOf<HostRecord>()
                for (i in 0 until recordsArray.length()) {
                    val recordObj = recordsArray.getJSONObject(i)

                    val name = recordObj.optString("name", "")
                    val provider = recordObj.optString("provider", "")
                    val primaryIp = recordObj.optString("primary_ip", "")
                    val meta = recordObj.optJSONObject("meta")
                    val backendName = meta?.optString("backend_name", "") ?: ""
                    val sshPort = meta?.optString("ssh_port", "")?.toIntOrNull() ?: 0

                    val groupsArray = recordObj.optJSONArray("groups")
                    val groups = mutableListOf<String>()
                    if (groupsArray != null) {
                        for (j in 0 until groupsArray.length()) {
                            groups.add(groupsArray.getString(j))
                        }
                    }

                    parsedRecords.add(
                        HostRecord(
                            name = name,
                            provider = provider,
                            groups = groups,
                            backendName = backendName,
                            primaryIp = primaryIp,
                            sshPort = sshPort,
                        ),
                    )
                }
                parsedRecords
            }

            override suspend fun exec(req: ExecRequest): List<ExecResult> = withContext(Dispatchers.IO) {
                val reqJson = JSONObject().apply {
                    put("config_path", configPath)
                    put("backends", req.backends.joinToString(","))
                    put("name", req.name)
                    put("name_regex", req.nameRegex)
                    put("providers", req.providers)
                    put("host", req.name)
                    put("host_ip", req.hostIp)
                    put("ssh_port", req.sshPort)
                    put("command", req.command)
                    put("ssh_user", req.sshUser)
                    put("ssh_identity_file", req.sshIdentityFile)
                    put("ssh_identity_passphrase", req.sshIdentityPassphrase)
                }.toString()

                val respJson = Mobile.exec(reqJson)
                val arr = JSONObject(respJson).optJSONArray("results") ?: JSONArray()
                (0 until arr.length()).map { i ->
                    val r = arr.getJSONObject(i)
                    ExecResult(
                        host = r.optString("host", ""),
                        output = r.optString("output", ""),
                        exit_code = r.optInt("exit_code", -1),
                        error = r.optString("error", "").ifEmpty { null }
                    )
                }
            }

            override suspend fun listRecipes(): List<Recipe> = emptyList()

            override suspend fun cueExec(req: CueExecRequest): CueExecResult = CueExecResult("Dummy plan")
        }
    }
}
