package com.honey.mobile.auth

import androidx.lifecycle.ViewModel
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import javax.inject.Inject

@HiltViewModel
class AuthViewModel @Inject constructor() : ViewModel() {
    private val _unlocked = MutableStateFlow(false)
    val unlocked = _unlocked.asStateFlow()

    fun unlock() { _unlocked.value = true }
    fun lock() { _unlocked.value = false }
}
