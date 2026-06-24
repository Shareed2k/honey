package com.honey.mobile.ui

import com.honey.mobile.api.Backend
import com.honey.mobile.api.HoneyApi
import com.honey.mobile.api.HostRecord
import com.honey.mobile.api.MetaResponse
import com.honey.mobile.api.SearchRequest
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.test.setMain
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Before
import org.junit.Test

@kotlinx.coroutines.ExperimentalCoroutinesApi
class DashboardViewModelTest {

    private val testDispatcher = StandardTestDispatcher()

    @Before
    fun setup() {
        Dispatchers.setMain(testDispatcher)
    }

    @After
    fun tearDown() {
        Dispatchers.resetMain()
    }

    @Test
    fun `search updates state with results`() = runTest {
        val mockApi = object : HoneyApi {
            override suspend fun getMeta() = MetaResponse("1.0.0")
            override suspend fun getBackends() = emptyList<Backend>()
            override suspend fun searchHosts(req: SearchRequest): List<HostRecord> {
                return listOf(HostRecord("web-1", "aws", emptyList()))
            }
            override suspend fun exec(req: com.honey.mobile.api.ExecRequest) = emptyList<com.honey.mobile.api.ExecResult>()
            override suspend fun listRecipes() = emptyList<com.honey.mobile.api.Recipe>()
            override suspend fun cueExec(req: com.honey.mobile.api.CueExecRequest) = com.honey.mobile.api.CueExecResult("")
        }

        val viewModel = DashboardViewModel(mockApi)
        
        // Initial state
        assertEquals("", viewModel.state.value.nameQuery)
        assertEquals(emptyList<HostRecord>(), viewModel.state.value.results)

        // Update queries
        viewModel.updateNameQuery("web")
        
        // Trigger search
        viewModel.search()
        testDispatcher.scheduler.advanceUntilIdle()

        // Check state
        assertEquals("web", viewModel.state.value.nameQuery)
        assertEquals(1, viewModel.state.value.results.size)
        assertEquals("web-1", viewModel.state.value.results[0].name)
    }
}
