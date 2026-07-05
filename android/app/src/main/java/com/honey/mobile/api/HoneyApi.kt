package com.honey.mobile.api

interface HoneyApi {
    suspend fun getMeta(): MetaResponse
    suspend fun getBackends(): List<Backend>
    suspend fun searchHosts(req: SearchRequest): List<HostRecord>
    suspend fun exec(req: ExecRequest): List<ExecResult>
    suspend fun listRecipes(): List<Recipe>
    suspend fun cueExec(req: CueExecRequest): CueExecResult
}
