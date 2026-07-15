package com.honey.mobile.vpn

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Intent
import android.content.pm.ServiceInfo
import android.net.VpnService
import android.os.Build
import android.os.ParcelFileDescriptor
import androidx.core.app.NotificationCompat
import androidx.core.app.ServiceCompat
import com.honey.mobile.MainActivity
import com.honey.mobile.R
import dagger.hilt.android.AndroidEntryPoint
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.launch
import mobile.Mobile
import mobile.VPNCallback
import org.json.JSONObject
import java.net.InetAddress
import javax.inject.Inject

/**
 * HoneyVpnService establishes the Android TUN, then hands its fd to the Go
 * tun2socks engine (Mobile.startVPN), which pumps packets through a SOCKS5
 * tunnel over SSH. The SSH carrier avoids a routing loop two ways: the exit IP
 * is excluded from the TUN routes, and this app is excluded from its own VPN.
 */
@AndroidEntryPoint
class HoneyVpnService : VpnService() {

    @Inject lateinit var controller: VpnController

    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private var tun: ParcelFileDescriptor? = null

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        when (intent?.action) {
            ACTION_STOP -> {
                teardown()
                return START_NOT_STICKY
            }
            else -> startTunnel()
        }
        return START_STICKY
    }

    private fun startTunnel() {
        val request = controller.pendingRequest
        if (request == null) {
            controller.onError("no connection request")
            stopSelf()
            return
        }
        startForegroundConnecting(request.exitName)
        controller.onResolving()

        scope.launch {
            try {
                val configPath = "${filesDir.absolutePath}/config/config.yaml"
                val resolveJson = JSONObject().apply {
                    put("config_path", configPath)
                    put("backends", request.backends)
                    put("name", request.exitName)
                    put("name_regex", request.nameRegex)
                    put("providers", request.providers)
                    put("host_ip", request.hostIp)
                    put("ssh_port", request.sshPort)
                    put("ssh_user", request.sshUser)
                }.toString()

                val info = JSONObject(Mobile.resolveExitNode(resolveJson))
                val exitName = info.optString("name", request.exitName)
                val routes = info.optJSONArray("tunnel_routes")

                val builder = Builder()
                    .setSession("Honey · $exitName")
                    .setMtu(MTU)
                    .addAddress("10.66.0.2", 32)
                    .addDnsServer("1.1.1.1")
                    .addDnsServer("8.8.8.8")

                // Route everything-except-exit-IP into the TUN.
                var routeCount = 0
                if (routes != null) {
                    for (i in 0 until routes.length()) {
                        val cidr = routes.getString(i)
                        if (addCidrRoute(builder, cidr)) routeCount++
                    }
                }
                if (routeCount == 0) {
                    builder.addRoute("0.0.0.0", 0) // fallback: full tunnel
                }

                // Belt-and-suspenders: keep this app's own SSH carrier off the VPN.
                runCatching { builder.addDisallowedApplication(packageName) }

                val pfd = builder.establish() ?: throw IllegalStateException("VpnService.establish returned null")
                tun = pfd

                controller.onConnecting()

                // Key was decrypted in the Activity (behind a biometric prompt) and
                // passed in-memory; the service never touches the keystore.
                val pem = request.sshKeyPem
                val passphrase = request.sshKeyPassphrase

                val startJson = JSONObject().apply {
                    put("config_path", configPath)
                    put("backends", request.backends)
                    put("name", request.exitName)
                    put("name_regex", request.nameRegex)
                    put("providers", request.providers)
                    put("host_ip", request.hostIp)
                    put("ssh_port", request.sshPort)
                    put("ssh_user", request.sshUser)
                    put("ssh_identity_file", pem)
                    put("ssh_identity_passphrase", passphrase)
                    put("mtu", MTU)
                }.toString()

                Mobile.startVPN(pfd.fd.toLong(), startJson, callback(exitName))
            } catch (e: Exception) {
                controller.onError(e.message ?: "connect failed")
                teardown()
            }
        }
    }

    private fun callback(exitName: String) = object : VPNCallback {
        override fun onState(state: String?) {
            when (state) {
                "connected" -> {
                    controller.onConnected(exitName)
                    updateNotification("Connected · $exitName")
                }
                "error" -> {
                    controller.onError("tunnel error")
                    teardown()
                }
                "disconnected", "stopping" -> controller.onDisconnected()
                "resolving" -> controller.onResolving()
                "connecting" -> controller.onConnecting()
            }
        }

        override fun onStats(statsJSON: String?) {
            statsJSON ?: return
            runCatching {
                val o = JSONObject(statsJSON)
                controller.onStats(
                    VpnStats(
                        upTotal = o.optLong("up_total"),
                        downTotal = o.optLong("down_total"),
                        upRate = o.optLong("up_rate"),
                        downRate = o.optLong("down_rate"),
                        uptimeSeconds = o.optLong("uptime_s"),
                    ),
                )
            }
        }
    }

    private fun teardown() {
        runCatching { Mobile.stopVPN() }
        runCatching { tun?.close() }
        tun = null
        if (controller.state.value !is VpnState.Error) {
            controller.onDisconnected()
        }
        ServiceCompat.stopForeground(this, ServiceCompat.STOP_FOREGROUND_REMOVE)
        stopSelf()
    }

    override fun onRevoke() {
        teardown()
        super.onRevoke()
    }

    override fun onDestroy() {
        scope.cancel()
        super.onDestroy()
    }

    // ── Notifications ─────────────────────────────────────────────────────────

    private fun startForegroundConnecting(exit: String) {
        ensureChannel()
        ServiceCompat.startForeground(
            this,
            NOTIF_ID,
            buildNotification("Connecting to $exit…"),
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.UPSIDE_DOWN_CAKE)
                ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE else 0,
        )
    }

    private fun updateNotification(text: String) {
        val nm = getSystemService(NotificationManager::class.java)
        nm.notify(NOTIF_ID, buildNotification(text))
    }

    private fun buildNotification(text: String): Notification {
        val open = PendingIntent.getActivity(
            this, 0, Intent(this, MainActivity::class.java),
            PendingIntent.FLAG_IMMUTABLE,
        )
        val stop = PendingIntent.getService(
            this, 1, Intent(this, HoneyVpnService::class.java).setAction(ACTION_STOP),
            PendingIntent.FLAG_IMMUTABLE,
        )
        return NotificationCompat.Builder(this, CHANNEL_ID)
            .setContentTitle("Honey VPN")
            .setContentText(text)
            .setSmallIcon(R.drawable.ic_vpn)
            .setOngoing(true)
            .setContentIntent(open)
            .addAction(0, "Disconnect", stop)
            .build()
    }

    private fun ensureChannel() {
        val nm = getSystemService(NotificationManager::class.java)
        if (nm.getNotificationChannel(CHANNEL_ID) == null) {
            nm.createNotificationChannel(
                NotificationChannel(CHANNEL_ID, "VPN", NotificationManager.IMPORTANCE_LOW),
            )
        }
    }

    // ── Routing helpers ───────────────────────────────────────────────────────

    /** addRoute for a "addr/prefix" CIDR; returns false on parse failure. */
    private fun addCidrRoute(builder: Builder, cidr: String): Boolean = runCatching {
        val slash = cidr.indexOf('/')
        if (slash <= 0) return false
        val addr = InetAddress.getByName(cidr.substring(0, slash))
        val prefix = cidr.substring(slash + 1).toInt()
        builder.addRoute(addr, prefix)
        true
    }.getOrDefault(false)

    companion object {
        const val ACTION_START = "com.honey.mobile.vpn.START"
        const val ACTION_STOP = "com.honey.mobile.vpn.STOP"
        private const val CHANNEL_ID = "honey_vpn"
        private const val NOTIF_ID = 0x4201
        private const val MTU = 1500
    }
}
