package com.honey.mobile.data

import android.util.Base64
import mobile.Mobile
import org.json.JSONObject
import java.util.UUID
import javax.crypto.Cipher
import javax.inject.Inject
import javax.inject.Singleton

/** Metadata describing a stored SSH key (never carries the PEM itself). */
data class SshKeyMeta(
    val id: String,
    val name: String,
    val type: String,
    val fingerprint: String,
    val hasPassphrase: Boolean,
    val passphraseSaved: Boolean,
)

/**
 * KeyStoreManager persists SSH private keys inside the encrypted [SecretsStore],
 * with the PEM (and any saved SSH passphrase) additionally sealed by the per-use,
 * auth-required [SshKeyVault]. Each record holds only ciphertext under
 * "sshkey:<id>"; importing and using a key each require a biometric/credential
 * authorization (driven by the caller via auth/KeyUnlock).
 *
 * Storage shape (JSON under "sshkey:<id>"):
 *   { name, type, fingerprint, hasPassphrase, passSaved, enc:{ iv, ct } }   // sealed
 * Legacy records written before this change carry a plaintext "pem" field and
 * an optional "sshkeypass:<id>" entry; they are read ungated until re-imported.
 */
@Singleton
class KeyStoreManager @Inject constructor(
    private val secrets: SecretsStore,
    private val vault: SshKeyVault,
) {
    private val keyPrefix = "sshkey:"
    private val passPrefix = "sshkeypass:" // legacy plaintext passphrase entries

    fun list(): List<SshKeyMeta> =
        secrets.keys()
            .filter { it.startsWith(keyPrefix) }
            .mapNotNull { storeKey ->
                val raw = secrets.get(storeKey) ?: return@mapNotNull null
                runCatching {
                    val o = JSONObject(raw)
                    val id = storeKey.removePrefix(keyPrefix)
                    val sealed = o.has("enc")
                    val passSaved = if (sealed) {
                        o.optBoolean("passSaved")
                    } else {
                        secrets.get(passPrefix + id) != null
                    }
                    SshKeyMeta(
                        id = id,
                        name = o.optString("name"),
                        type = o.optString("type"),
                        fingerprint = o.optString("fingerprint"),
                        hasPassphrase = o.optBoolean("hasPassphrase"),
                        passphraseSaved = passSaved,
                    )
                }.getOrNull()
            }
            .sortedBy { it.name.lowercase() }

    // ── Import (two phases: validate, then store after authorized encrypt) ──────

    /** Result of [prepareImport]: validated key material awaiting an authorized seal. */
    data class PendingImport(
        val name: String,
        val type: String,
        val fingerprint: String,
        val hasPassphrase: Boolean,
        val savePassphrase: Boolean,
        val plaintext: ByteArray, // {"pem":…,"pass":…}
    )

    /**
     * Validates the key via Go (throws on bad key/passphrase) and packs the PEM +
     * (optionally saved) passphrase for sealing. No storage, no authentication.
     */
    fun prepareImport(name: String, pem: String, passphrase: String, savePassphrase: Boolean): PendingImport {
        val info = JSONObject(Mobile.keyFingerprint(pem, passphrase))
        val hasPassphrase = passphrase.isNotEmpty()
        val plaintext = JSONObject().apply {
            put("pem", pem)
            put("pass", if (hasPassphrase && savePassphrase) passphrase else "")
        }.toString().toByteArray(Charsets.UTF_8)
        return PendingImport(
            name = name.ifBlank { info.optString("type", "key") },
            type = info.optString("type"),
            fingerprint = info.optString("fingerprint"),
            hasPassphrase = hasPassphrase,
            savePassphrase = hasPassphrase && savePassphrase,
            plaintext = plaintext,
        )
    }

    /** Cipher to authorize (encrypt) before [finishImport]. */
    fun encryptCipher(): Cipher = vault.encryptCipher()

    /** Seals the prepared key with the authorized cipher and persists it. */
    fun finishImport(p: PendingImport, authorizedCipher: Cipher): SshKeyMeta {
        val blob = vault.seal(authorizedCipher, p.plaintext)
        val id = UUID.randomUUID().toString()
        val record = JSONObject().apply {
            put("name", p.name)
            put("type", p.type)
            put("fingerprint", p.fingerprint)
            put("hasPassphrase", p.hasPassphrase)
            put("passSaved", p.savePassphrase)
            put("enc", JSONObject().apply {
                put("iv", b64(blob.iv))
                put("ct", b64(blob.ct))
            })
        }
        secrets.put(keyPrefix + id, record.toString())
        return SshKeyMeta(id, p.name, p.type, p.fingerprint, p.hasPassphrase, p.savePassphrase)
    }

    // ── Unlock for use (two phases: begin, then open after authorized decrypt) ──

    sealed interface UnlockStart {
        /** Legacy plaintext key (pre-upgrade): no authorization needed. */
        data class Ready(val pem: String, val passphrase: String) : UnlockStart

        /** Sealed key: authorize [cipher], then call [completeUnlock] with it. */
        data class NeedAuth(val cipher: Cipher, val ct: ByteArray) : UnlockStart

        object NotFound : UnlockStart
    }

    fun beginUnlock(id: String): UnlockStart {
        val raw = secrets.get(keyPrefix + id) ?: return UnlockStart.NotFound
        val o = runCatching { JSONObject(raw) }.getOrNull() ?: return UnlockStart.NotFound
        val enc = o.optJSONObject("enc")
        if (enc != null) {
            val iv = unb64(enc.optString("iv"))
            val ct = unb64(enc.optString("ct"))
            return UnlockStart.NeedAuth(vault.decryptCipher(iv), ct)
        }
        // Legacy plaintext record (written before sealing existed).
        val pem = o.optString("pem")
        if (pem.isEmpty()) return UnlockStart.NotFound
        return UnlockStart.Ready(pem, secrets.get(passPrefix + id) ?: "")
    }

    /** Opens a [UnlockStart.NeedAuth] with the authorized cipher → (pem, passphrase). */
    fun completeUnlock(start: UnlockStart.NeedAuth, authorizedCipher: Cipher): Pair<String, String> {
        val plain = vault.open(authorizedCipher, start.ct)
        val o = JSONObject(String(plain, Charsets.UTF_8))
        return o.optString("pem") to o.optString("pass")
    }

    fun delete(id: String) {
        secrets.delete(keyPrefix + id)
        secrets.delete(passPrefix + id)
    }

    private fun b64(b: ByteArray): String = Base64.encodeToString(b, Base64.NO_WRAP)
    private fun unb64(s: String): ByteArray = Base64.decode(s, Base64.NO_WRAP)
}
