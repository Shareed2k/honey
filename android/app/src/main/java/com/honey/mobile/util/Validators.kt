package com.honey.mobile.util

import android.util.Patterns

object Validators {
    fun isValidUrl(url: String): Boolean {
        return url.isBlank() || Patterns.WEB_URL.matcher(url).matches()
    }

    fun isValidIp(ip: String): Boolean {
        return ip.isBlank() || Patterns.IP_ADDRESS.matcher(ip).matches()
    }
}
