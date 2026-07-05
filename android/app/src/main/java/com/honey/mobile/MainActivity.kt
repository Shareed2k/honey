package com.honey.mobile

import android.net.Uri
import android.os.Bundle
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.appcompat.app.AppCompatActivity
import androidx.compose.foundation.layout.*
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Menu
import androidx.compose.material.icons.outlined.Dns
import androidx.compose.material.icons.outlined.Key
import androidx.compose.material.icons.outlined.Lock
import androidx.compose.material.icons.outlined.Search
import androidx.compose.material.icons.outlined.QrCodeScanner
import androidx.compose.material.icons.outlined.Settings
import androidx.compose.material.icons.outlined.Storage
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.navigation.NavType
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.currentBackStackEntryAsState
import androidx.navigation.compose.rememberNavController
import androidx.navigation.navArgument
import com.honey.mobile.auth.AuthScreen
import com.honey.mobile.auth.AuthViewModel
import com.honey.mobile.auth.BiometricHelper
import com.honey.mobile.ui.*
import com.honey.mobile.ui.theme.HoneyTheme
import com.honey.mobile.ui.theme.NeonCyan
import dagger.hilt.android.AndroidEntryPoint
import kotlinx.coroutines.launch

@AndroidEntryPoint
class MainActivity : AppCompatActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()
        setContent {
            HoneyTheme {
                val authVm: AuthViewModel = hiltViewModel()
                val unlocked by authVm.unlocked.collectAsStateWithLifecycle()
                if (unlocked) {
                    HoneyNavApp()
                } else {
                    var authError by remember { mutableStateOf<String?>(null) }
                    AuthScreen(
                        error = authError,
                        onRequestAuth = {
                            BiometricHelper.prompt(
                                activity = this@MainActivity,
                                onSuccess = {
                                    authError = null
                                    authVm.unlock()
                                },
                                onFail = { error -> authError = error },
                            )
                        },
                    )
                }
            }
        }
    }
}

private data class NavDest(val route: String, val label: String, val icon: ImageVector)

// Drawer destinations. Exec and VPN are intentionally absent: they are reachable
// only by tapping a host in the dashboard, which supplies the host + IP directly.
private val navDests = listOf(
    NavDest("dashboard", "Dashboard", Icons.Outlined.Search),
    NavDest("recipes", "Recipes", Icons.Outlined.Storage),
    NavDest("keys", "SSH Keys", Icons.Outlined.Key),
    NavDest("secrets", "Secrets", Icons.Outlined.Lock),
    NavDest("backends", "Backends", Icons.Outlined.Dns),
    NavDest("enroll", "Enroll device", Icons.Outlined.QrCodeScanner),
    NavDest("config", "Config", Icons.Outlined.Settings),
)

// Top-bar titles for every route, including the record-only exec/vpn screens
// that don't appear in the drawer.
private fun titleFor(route: String): String = when (route) {
    "vpn" -> "VPN"
    "exec" -> "Exec"
    else -> navDests.firstOrNull { it.route == route }?.label ?: "Honey"
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun HoneyNavApp() {
    val navController = rememberNavController()
    val drawerState = rememberDrawerState(DrawerValue.Closed)
    val scope = rememberCoroutineScope()
    val backStack by navController.currentBackStackEntryAsState()
    val currentRoute = backStack?.destination?.route?.substringBefore("?") ?: "dashboard"

    ModalNavigationDrawer(
        drawerState = drawerState,
        drawerContent = {
            ModalDrawerSheet(drawerContainerColor = MaterialTheme.colorScheme.surface) {
                Spacer(Modifier.height(28.dp))
                Text(
                    "  honey",
                    style = MaterialTheme.typography.headlineMedium,
                    color = NeonCyan,
                    modifier = Modifier.padding(start = 16.dp, bottom = 12.dp),
                )
                navDests.forEach { dest ->
                    NavigationDrawerItem(
                        icon = { Icon(dest.icon, contentDescription = dest.label) },
                        label = { Text(dest.label) },
                        selected = currentRoute == dest.route,
                        onClick = {
                            navController.navigate(dest.route) {
                                popUpTo(navController.graph.startDestinationId) {
                                    saveState = true
                                }
                                launchSingleTop = true
                                restoreState = true
                            }
                            scope.launch { drawerState.close() }
                        },
                        modifier = Modifier.padding(NavigationDrawerItemDefaults.ItemPadding),
                    )
                }
            }
        },
    ) {
        Scaffold(
            topBar = {
                TopAppBar(
                    title = {
                        Text(titleFor(currentRoute))
                    },
                    navigationIcon = {
                        IconButton(onClick = { scope.launch { drawerState.open() } }) {
                            Icon(Icons.Default.Menu, contentDescription = "Menu")
                        }
                    },
                    colors = TopAppBarDefaults.topAppBarColors(
                        containerColor = MaterialTheme.colorScheme.background,
                        titleContentColor = MaterialTheme.colorScheme.onBackground,
                    ),
                )
            },
        ) { padding ->
            NavHost(navController, startDestination = "dashboard", Modifier.padding(padding)) {
                composable(
                    route = "vpn?exit={exit}&ip={ip}&sshport={sshport}",
                    arguments = listOf(
                        navArgument("exit") {
                            type = NavType.StringType
                            defaultValue = ""
                        },
                        navArgument("ip") {
                            type = NavType.StringType
                            defaultValue = ""
                        },
                        navArgument("sshport") {
                            type = NavType.IntType
                            defaultValue = 0
                        },
                    ),
                ) { backStackEntry ->
                    VpnScreen(
                        prefilledExit = backStackEntry.arguments?.getString("exit") ?: "",
                        prefilledIp = backStackEntry.arguments?.getString("ip") ?: "",
                        prefilledSshPort = backStackEntry.arguments?.getInt("sshport") ?: 0,
                    )
                }
                composable("dashboard") {
                    DashboardScreen(
                        onNavigateExec = { hostName, provider, ip, sshPort ->
                            navController.navigate(
                                "exec?host=$hostName&provider=$provider&ip=${Uri.encode(ip)}&sshport=$sshPort",
                            ) { launchSingleTop = true }
                        },
                        onNavigateVpn = { hostName, ip, sshPort ->
                            navController.navigate(
                                "vpn?exit=$hostName&ip=${Uri.encode(ip)}&sshport=$sshPort",
                            ) { launchSingleTop = true }
                        },
                    )
                }
                composable("backends") {
                    BackendsScreen()
                }
                composable(
                    route = "exec?host={host}&provider={provider}&ip={ip}&sshport={sshport}",
                    arguments = listOf(
                        navArgument("host") {
                            type = NavType.StringType
                            defaultValue = ""
                        },
                        navArgument("provider") {
                            type = NavType.StringType
                            defaultValue = ""
                        },
                        navArgument("ip") {
                            type = NavType.StringType
                            defaultValue = ""
                        },
                        navArgument("sshport") {
                            type = NavType.IntType
                            defaultValue = 0
                        },
                    ),
                ) { backStackEntry ->
                    ExecScreen(
                        prefilledHost = backStackEntry.arguments?.getString("host") ?: "",
                        prefilledProvider = backStackEntry.arguments?.getString("provider") ?: "",
                        prefilledIp = backStackEntry.arguments?.getString("ip") ?: "",
                        prefilledSshPort = backStackEntry.arguments?.getInt("sshport") ?: 0,
                    )
                }
                composable("recipes") { RecipesScreen() }
                composable("enroll") { EnrollScreen() }
                composable("keys") { KeysScreen() }
                composable("secrets") { SecretsScreen() }
                composable("config") { ConfigScreen() }
            }
        }
    }
}
