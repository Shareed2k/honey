package com.honey.mobile.api

import retrofit2.http.*

interface HoneyApi {
    @GET("/api/v1/meta")
    suspend fun getMeta(): MetaResponse

    @GET("/api/v1/backends")
    suspend fun getBackends(): List<Backend>

    @POST("/api/v1/search")
    suspend fun searchHosts(@Body req: SearchRequest): List<HostRecord>

    @POST("/api/v1/exec")
    suspend fun exec(@Body req: ExecRequest): List<ExecResult>

    @GET("/api/v1/recipes")
    suspend fun listRecipes(): List<Recipe>

    @POST("/api/v1/cue-exec")
    suspend fun cueExec(@Body req: CueExecRequest): CueExecResult
}
