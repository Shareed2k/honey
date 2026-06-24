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
        // TODO: Replace dummy implementation with native Mobile AAR calls.
        // This is necessary to keep the app compiling, but currently leaves other screens silently broken.
        return object : HoneyApi {
            override suspend fun getMeta(): MetaResponse = throw NotImplementedError("Pending AAR migration")
            override suspend fun getBackends(): List<Backend> = throw NotImplementedError("Pending AAR migration")
            override suspend fun searchHosts(req: SearchRequest): List<HostRecord> = throw NotImplementedError("Pending AAR migration")
            override suspend fun exec(req: ExecRequest): List<ExecResult> = throw NotImplementedError("Pending AAR migration")
            override suspend fun listRecipes(): List<Recipe> = throw NotImplementedError("Pending AAR migration")
            override suspend fun cueExec(req: CueExecRequest): CueExecResult = throw NotImplementedError("Pending AAR migration")
        }
    }
}
