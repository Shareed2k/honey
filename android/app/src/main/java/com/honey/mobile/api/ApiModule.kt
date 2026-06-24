package com.honey.mobile.api

import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.components.SingletonComponent
import javax.inject.Singleton

@Module
@InstallIn(SingletonComponent::class)
object ApiModule {
    @Provides
    @Singleton
    fun provideHoneyApi(): HoneyApi {
        return object : HoneyApi {
            override suspend fun getMeta(): MetaResponse = MetaResponse("0.1.0-mobile")
            override suspend fun getBackends(): List<Backend> = emptyList()
            override suspend fun searchHosts(req: SearchRequest): List<HostRecord> = emptyList()
            override suspend fun exec(req: ExecRequest): List<ExecResult> = emptyList()
            override suspend fun listRecipes(): List<Recipe> = emptyList()
            override suspend fun cueExec(req: CueExecRequest): CueExecResult = CueExecResult("")
        }
    }
}
