package com.honey.mobile.util

import android.util.Patterns

object Validators {
    fun isValidUrl(url: String): Boolean {
        if (url.isBlank()) return true
        if (Patterns.WEB_URL.matcher(url).matches()) return true
        // Fallback for homelab/infrastructure tools
        val permissiveRegex = "^[a-zA-Z0-9.-]+(:[0-9]+)?$".toRegex()
        return permissiveRegex.matches(url)
    }

    fun isValidIp(ip: String): Boolean {
        return ip.isBlank() || Patterns.IP_ADDRESS.matcher(ip).matches()
    }
}
