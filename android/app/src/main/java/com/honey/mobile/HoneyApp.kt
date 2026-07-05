package com.honey.mobile

import android.app.Application
import com.honey.mobile.data.DeviceEnrollment
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

        // If a device cert was enrolled, hand it to the engine so mTLS honey
        // backends work from launch (private key stays in the Keystore).
        try {
            DeviceEnrollment(this).registerWithEngine()
        } catch (e: Exception) {
            e.printStackTrace()
        }
    }
}
