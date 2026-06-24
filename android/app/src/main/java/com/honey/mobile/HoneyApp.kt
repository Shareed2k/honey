package com.honey.mobile

import android.app.Application
import dagger.hilt.android.HiltAndroidApp
import mobile.Mobile

@HiltAndroidApp
class HoneyApp : Application() {
    override fun onCreate() {
        super.onCreate()
        
        val homeDir = filesDir.absolutePath
        val configDir = "${filesDir.absolutePath}/config"
        val cacheDir = externalCacheDir?.absolutePath ?: cacheDir.absolutePath
        val recordDir = "${getExternalFilesDir(null)?.absolutePath}/recordings"
        val recipesDir = "${getExternalFilesDir(null)?.absolutePath}/recipes"
        try {
            Mobile.initDefaultConfig(homeDir, configDir, cacheDir, recordDir, recipesDir)
        } catch (e: Exception) {
            e.printStackTrace()
        }
    }
}
