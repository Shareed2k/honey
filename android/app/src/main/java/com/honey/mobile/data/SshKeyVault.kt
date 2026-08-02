package com.honey.mobile.data

import android.os.Build
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import java.security.KeyStore
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec
import javax.inject.Inject
import javax.inject.Singleton

/**
 * SshKeyVault wraps SSH private-key material with a per-use, auth-required
 * AES-256-GCM key held in the AndroidKeyStore (alias [ALIAS]). Every encrypt and
 * decrypt operation must be authorized by a fresh user authentication, surfaced
 * via a BiometricPrompt CryptoObject (see auth/KeyUnlock).
 *
 * Encrypt (import) and decrypt (use) both go through the prompt: callers obtain
 * an uninitialized-for-auth [Cipher] from [encryptCipher]/[decryptCipher], have
 * the user authenticate it, then call [seal]/[open] with the authorized cipher.
 */
@Singleton
class SshKeyVault @Inject constructor() {
    private val keyStore = KeyStore.getInstance(ANDROID_KEYSTORE).apply { load(null) }

    /** Ciphertext bundle: GCM nonce + ciphertext (incl. auth tag). */
    data class Blob(val iv: ByteArray, val ct: ByteArray)

    private fun secretKey(): SecretKey {
        (keyStore.getKey(ALIAS, null) as? SecretKey)?.let { return it }
        val kg = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, ANDROID_KEYSTORE)
        val spec = KeyGenParameterSpec.Builder(
            ALIAS,
            KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT,
        )
            .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
            .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
            .setKeySize(256)
            .setUserAuthenticationRequired(true)
            .apply {
                if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) {
                    // timeout 0 => authentication required for every operation.
                    setUserAuthenticationParameters(
                        0,
                        KeyProperties.AUTH_BIOMETRIC_STRONG or KeyProperties.AUTH_DEVICE_CREDENTIAL,
                    )
                } else {
                    @Suppress("DEPRECATION")
                    setUserAuthenticationValidityDurationSeconds(-1) // per-operation (biometric)
                }
                // Keep stored keys usable when the user enrolls a new fingerprint;
                // the alternative silently destroys them and forces re-import.
                setInvalidatedByBiometricEnrollment(false)
            }
            .build()
        kg.init(spec)
        return kg.generateKey()
    }

    /** Cipher to authorize before [seal]. The Keystore generates the GCM nonce. */
    fun encryptCipher(): Cipher =
        Cipher.getInstance(TRANSFORM).apply { init(Cipher.ENCRYPT_MODE, secretKey()) }

    /** Cipher to authorize before [open]. */
    fun decryptCipher(iv: ByteArray): Cipher =
        Cipher.getInstance(TRANSFORM).apply {
            init(Cipher.DECRYPT_MODE, secretKey(), GCMParameterSpec(GCM_TAG_BITS, iv))
        }

    fun seal(authorized: Cipher, plaintext: ByteArray): Blob =
        Blob(iv = authorized.iv, ct = authorized.doFinal(plaintext))

    fun open(authorized: Cipher, ct: ByteArray): ByteArray = authorized.doFinal(ct)

    /** Drops the key (e.g. after KeyPermanentlyInvalidatedException); stored blobs become unrecoverable. */
    fun reset() {
        if (keyStore.containsAlias(ALIAS)) keyStore.deleteEntry(ALIAS)
    }

    companion object {
        private const val ANDROID_KEYSTORE = "AndroidKeyStore"
        private const val ALIAS = "honey_ssh_v1"
        private const val TRANSFORM = "AES/GCM/NoPadding"
        private const val GCM_TAG_BITS = 128
    }
}
