package com.honey.mobile.data

import android.content.Context
import androidx.security.crypto.EncryptedSharedPreferences
import androidx.security.crypto.MasterKey
import dagger.hilt.android.qualifiers.ApplicationContext
import javax.inject.Inject
import javax.inject.Singleton

@Singleton
class SecretsStore @Inject constructor(@ApplicationContext private val context: Context) {

    private val prefs by lazy {
        val master = MasterKey.Builder(context)
            .setKeyScheme(MasterKey.KeyScheme.AES256_GCM)
            .build()
        EncryptedSharedPreferences.create(
            context, "honey_secrets", master,
            EncryptedSharedPreferences.PrefKeyEncryptionScheme.AES256_SIV,
            EncryptedSharedPreferences.PrefValueEncryptionScheme.AES256_GCM
        )
    }

    fun put(key: String, value: String) = prefs.edit().putString(key, value).apply()
    fun get(key: String): String? = prefs.getString(key, null)
    fun delete(key: String) = prefs.edit().remove(key).apply()
    fun keys(): Set<String> = prefs.all.keys

    /** Replace {{KEY}} placeholders in [text] with stored secrets. */
    fun resolve(text: String): String {
        var result = text
        Regex("""\{\{(\w+)\}\}""").findAll(text).forEach { m ->
            val value = get(m.groupValues[1]) ?: return@forEach
            result = result.replace(m.value, value)
        }
        return result
    }
}
