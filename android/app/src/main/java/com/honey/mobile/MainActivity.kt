package com.honey.mobile

import android.content.Intent
import android.os.Bundle
import androidx.appcompat.app.AppCompatActivity
import androidx.activity.compose.setContent
import androidx.compose.foundation.layout.*
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Menu
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.rememberNavController
import com.honey.mobile.auth.AuthScreen
import com.honey.mobile.auth.AuthViewModel
import com.honey.mobile.auth.BiometricHelper
import com.honey.mobile.ui.*
import dagger.hilt.android.AndroidEntryPoint
import kotlinx.coroutines.launch

@AndroidEntryPoint
class MainActivity : AppCompatActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        startForegroundService(Intent(this, HoneyService::class.java))
        setContent {
            MaterialTheme {
                val authVm: AuthViewModel = hiltViewModel()
                val unlocked by authVm.unlocked.collectAsStateWithLifecycle()
                if (unlocked) {
                    HoneyNavApp()
                } else {
                    AuthScreen(onRequestAuth = {
                        BiometricHelper.prompt(
                            activity = this,
                            onSuccess = { authVm.unlock() },
                            onFail = { /* stays on auth screen */ }
                        )
                    })
                }
            }
        }
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun HoneyNavApp() {
    val navController = rememberNavController()
    val drawerState = rememberDrawerState(DrawerValue.Closed)
    val scope = rememberCoroutineScope()
    val items = listOf("dashboard", "backends", "exec", "recipes")

    ModalNavigationDrawer(
        drawerState = drawerState,
        drawerContent = {
            ModalDrawerSheet {
                Spacer(Modifier.height(16.dp))
                items.forEach { route ->
                    NavigationDrawerItem(
                        label = { Text(route.replaceFirstChar { it.uppercase() }) },
                        selected = false,
                        onClick = {
                            navController.navigate(route) { launchSingleTop = true }
                            scope.launch { drawerState.close() }
                        },
                        modifier = Modifier.padding(NavigationDrawerItemDefaults.ItemPadding)
                    )
                }
            }
        }
    ) {
        Scaffold(
            topBar = {
                TopAppBar(
                    title = { Text("Honey") },
                    navigationIcon = {
                        IconButton(onClick = { scope.launch { drawerState.open() } }) {
                            Icon(Icons.Default.Menu, contentDescription = "Menu")
                        }
                    }
                )
            }
        ) { padding ->
            NavHost(navController, startDestination = "dashboard", Modifier.padding(padding)) {
                composable("dashboard") { DashboardScreen() }
                composable("backends") { BackendsScreen() }
                composable("exec") { ExecScreen() }
                composable("recipes") { RecipesScreen() }
            }
        }
    }
}
