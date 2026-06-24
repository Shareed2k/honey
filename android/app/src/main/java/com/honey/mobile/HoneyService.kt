package com.honey.mobile

import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.Service
import android.content.Context
import android.content.Intent
import android.os.IBinder
import androidx.core.app.NotificationCompat
import dagger.hilt.android.AndroidEntryPoint
import java.io.File

@AndroidEntryPoint
class HoneyService : Service() {

    private lateinit var honeyProcess: HoneyProcess

    override fun onCreate() {
        super.onCreate()
        createNotificationChannel()
        honeyProcess = HoneyProcess(this)
        startForeground(NOTIFICATION_ID, buildNotification())
        val configDir = File(filesDir, "config").also { it.mkdirs() }
        honeyProcess.start(configDir)
            .onFailure { android.util.Log.e(TAG, "honey start failed", it) }
    }

    override fun onDestroy() {
        honeyProcess.stop()
        super.onDestroy()
    }

    override fun onBind(intent: Intent?): IBinder? = null

    private fun createNotificationChannel() {
        val channel = NotificationChannel(
            CHANNEL_ID, "Honey Server", NotificationManager.IMPORTANCE_LOW
        )
        val nm = getSystemService(Context.NOTIFICATION_SERVICE) as NotificationManager
        nm.createNotificationChannel(channel)
    }

    private fun buildNotification() = NotificationCompat.Builder(this, CHANNEL_ID)
        .setContentTitle("Honey")
        .setContentText("Running on :${honeyProcess.port}")
        .setSmallIcon(android.R.drawable.ic_menu_manage)
        .setOngoing(true)
        .build()

    companion object {
        private const val TAG = "HoneyService"
        private const val CHANNEL_ID = "honey_server"
        private const val NOTIFICATION_ID = 1
    }
}
