package com.honey.mobile.auth

import android.os.Build
import androidx.biometric.BiometricManager.Authenticators.BIOMETRIC_STRONG
import androidx.biometric.BiometricManager.Authenticators.DEVICE_CREDENTIAL
import androidx.biometric.BiometricPrompt
import androidx.core.content.ContextCompat
import androidx.fragment.app.FragmentActivity
import javax.crypto.Cipher
import kotlin.coroutines.resume
import kotlin.coroutines.resumeWithException
import kotlinx.coroutines.suspendCancellableCoroutine

/** Thrown when the user cancels or fails the key-unlock prompt. */
class KeyUnlockCancelled(message: String) : Exception(message)

/**
 * KeyUnlock drives a BiometricPrompt bound to a [Cipher] CryptoObject so a
 * per-use AndroidKeyStore key (see data/SshKeyVault) can perform one authorized
 * crypto operation. Returns the authorized cipher.
 *
 * Device-credential (PIN/pattern) as a CryptoObject fallback requires API 30+;
 * on older versions only a strong biometric can authorize the operation.
 */
object KeyUnlock {
    suspend fun authorize(
        activity: FragmentActivity,
        cipher: Cipher,
        title: String,
        subtitle: String,
    ): Cipher = suspendCancellableCoroutine { cont ->
        val executor = ContextCompat.getMainExecutor(activity)
        val callback = object : BiometricPrompt.AuthenticationCallback() {
            override fun onAuthenticationSucceeded(result: BiometricPrompt.AuthenticationResult) {
                val authed = result.cryptoObject?.cipher
                if (authed != null) {
                    cont.resume(authed)
                } else {
                    cont.resumeWithException(KeyUnlockCancelled("no authorized cipher"))
                }
            }

            override fun onAuthenticationError(code: Int, msg: CharSequence) {
                cont.resumeWithException(KeyUnlockCancelled(msg.toString()))
            }

            override fun onAuthenticationFailed() {
                // Non-terminal (e.g. one bad fingerprint); the prompt stays open.
            }
        }

        val prompt = BiometricPrompt(activity, executor, callback)
        val builder = BiometricPrompt.PromptInfo.Builder()
            .setTitle(title)
            .setSubtitle(subtitle)
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) {
            builder.setAllowedAuthenticators(BIOMETRIC_STRONG or DEVICE_CREDENTIAL)
        } else {
            builder.setAllowedAuthenticators(BIOMETRIC_STRONG)
            builder.setNegativeButtonText("Cancel")
        }
        prompt.authenticate(builder.build(), BiometricPrompt.CryptoObject(cipher))
    }
}
