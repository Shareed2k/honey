package com.honey.mobile.data

import android.content.Context
import com.google.gson.Gson
import dagger.hilt.android.qualifiers.ApplicationContext
import mobile.Mobile
import javax.inject.Inject
import javax.inject.Singleton

@Singleton
class ConfigRepository @Inject constructor(@ApplicationContext private val context: Context) {
    private val configDir: String get() = "${context.filesDir.absolutePath}/config"
    private val gson = Gson()

    fun load(): HoneyConfig = try {
        val json = Mobile.loadConfig(configDir)
        gson.fromJson(json, HoneyConfig::class.java) ?: HoneyConfig()
    } catch (e: Exception) {
        HoneyConfig()
    }

    fun save(config: HoneyConfig) {
        val json = gson.toJson(config)
        Mobile.saveConfig(configDir, json)
    }
}
