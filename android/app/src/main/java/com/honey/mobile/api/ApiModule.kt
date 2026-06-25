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
        // TODO: Replace dummy implementation with native Mobile AAR calls.
        // This is necessary to keep the app compiling, but currently leaves other screens silently broken.
        return object : HoneyApi {
            override suspend fun getMeta(): MetaResponse = MetaResponse("dummy")
            override suspend fun getBackends(): List<Backend> = withContext(Dispatchers.IO) {
                val reqJson = JSONObject().apply {
                    put("config_path", "${context.filesDir.absolutePath}/config/config.yaml")
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
                    put("config_path", "${context.filesDir.absolutePath}/config/config.yaml")
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
                    
                    val groupsArray = recordObj.optJSONArray("groups")
                    val groups = mutableListOf<String>()
                    if (groupsArray != null) {
                        for (j in 0 until groupsArray.length()) {
                            groups.add(groupsArray.getString(j))
                        }
                    }
                    
                    parsedRecords.add(HostRecord(name = name, provider = provider, groups = groups))
                }
                parsedRecords
            }
            override suspend fun exec(req: ExecRequest): List<ExecResult> = emptyList()
            override suspend fun listRecipes(): List<Recipe> = emptyList()
            override suspend fun cueExec(req: CueExecRequest): CueExecResult = CueExecResult("Dummy plan")
        }
    }
}
