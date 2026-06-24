package com.honey.mobile

import android.app.Application
import android.system.Os
import dagger.hilt.android.HiltAndroidApp
import mobile.Mobile

@HiltAndroidApp
class HoneyApp : Application() {
    override fun onCreate() {
        super.onCreate()
        
        try {
            Os.setenv("HOME", filesDir.absolutePath, true)
        } catch (e: Exception) {
            e.printStackTrace()
        }
        
        val configDir = "${filesDir.absolutePath}/config"
        val cacheDir = externalCacheDir?.absolutePath ?: cacheDir.absolutePath
        val recordDir = "${getExternalFilesDir(null)?.absolutePath}/recordings"
        val recipesDir = "${getExternalFilesDir(null)?.absolutePath}/recipes"
        
        try {
            Mobile.initDefaultConfig(configDir, cacheDir, recordDir, recipesDir)
        } catch (e: Exception) {
            e.printStackTrace()
        }
    }
}
