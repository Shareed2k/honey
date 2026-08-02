package com.honey.mobile.data

import android.content.Context
import android.os.Build
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import mobile.Mobile
import mobile.MTLSSigner
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import org.bouncycastle.asn1.x500.X500Name
import org.bouncycastle.openssl.jcajce.JcaPEMWriter
import org.bouncycastle.operator.ContentSigner
import org.bouncycastle.operator.DefaultSignatureAlgorithmIdentifierFinder
import org.bouncycastle.pkcs.jcajce.JcaPKCS10CertificationRequestBuilder
import org.json.JSONObject
import java.io.ByteArrayInputStream
import java.io.ByteArrayOutputStream
import java.io.File
import java.io.IOException
import java.io.OutputStream
import java.io.StringWriter
import java.net.Socket
import java.security.KeyPairGenerator
import java.security.KeyStore
import java.security.Principal
import java.security.PrivateKey
import java.security.Signature
import java.security.cert.CertificateFactory
import java.security.cert.X509Certificate
import java.security.spec.ECGenParameterSpec
import javax.net.ssl.KeyManager
import javax.net.ssl.SSLContext
import javax.net.ssl.TrustManagerFactory
import javax.net.ssl.X509ExtendedKeyManager
import javax.net.ssl.X509TrustManager

/**
 * DeviceEnrollment provisions and holds the device's mTLS client credential:
 *  - an EC P-256 key generated in the Android Keystore (StrongBox when available,
 *    biometric-gated), which never leaves the TEE;
 *  - a client certificate issued by honey's device CA in exchange for a CSR.
 *
 * The issued cert chain is stored in app-internal files (the Keystore won't let us
 * re-associate a CA-issued chain to an existing key entry), and paired with the
 * Keystore private key at TLS time via a custom X509KeyManager.
 */
class DeviceEnrollment(private val context: Context) {

    private val keyStore: KeyStore = KeyStore.getInstance(ANDROID_KEYSTORE).apply { load(null) }

    fun isEnrolled(): Boolean = keyStore.containsAlias(ALIAS) && chainFile().exists()

    /**
     * registerWithEngine hands the device credential to the in-process Go engine so
     * its honeyprovider can reach mTLS honey backends. The private key stays in the
     * Keystore/TEE — only signatures cross via the [mtlsSigner] callback.
     */
    fun registerWithEngine() {
        val signer = mtlsSigner() ?: return
        val chain = chainFile().takeIf { it.exists() }?.readText() ?: return
        Mobile.setDeviceMTLS(chain, caPem() ?: "", signer)
    }

    /** mtlsSigner signs a TLS digest with the Keystore key ("NONEwithECDSA" over the
     *  already-hashed input), returning ASN.1 DER — what Go's TLS stack expects. */
    private fun mtlsSigner(): MTLSSigner? {
        if (!keyStore.containsAlias(ALIAS)) return null
        val pk = keyStore.getKey(ALIAS, null) as? PrivateKey ?: return null
        return MTLSSigner { digest ->
            Signature.getInstance("NONEwithECDSA").run {
                initSign(pk)
                update(digest)
                sign()
            }
        }
    }

    /** enroll runs keygen (if needed) + CSR + the enroll exchange, storing the cert. */
    suspend fun enroll(enrollUrl: String, code: String, cn: String, caPinPem: String?): Unit =
        withContext(Dispatchers.IO) {
            generateKeyIfNeeded()
            val csrPem = buildCsr(cn)

            val bodyJson = JSONObject().put("code", code).put("csr", csrPem).toString()
            val client = enrollClient(caPinPem)
            val req = Request.Builder()
                .url(enrollUrl)
                .post(bodyJson.toRequestBody("application/json; charset=utf-8".toMediaType()))
                .build()
            client.newCall(req).execute().use { resp ->
                val raw = resp.body?.string().orEmpty()
                if (!resp.isSuccessful) throw IOException("enroll: HTTP ${resp.code}: $raw")
                val obj = JSONObject(raw)
                val cert = obj.optString("cert", "")
                val ca = obj.optString("ca", "")
                if (cert.isEmpty()) throw IOException("enroll: empty cert in response")
                // Store the client cert followed by the CA (the chain presented at TLS).
                chainFile().writeText(cert.trimEnd() + "\n" + ca.trimEnd() + "\n")
            }
            // Make the new credential usable by the in-process engine immediately.
            registerWithEngine()
        }

    /** clientKeyManagers pairs the stored cert chain with the Keystore private key. */
    fun clientKeyManagers(): Array<KeyManager>? {
        if (!isEnrolled()) return null
        val chain = readChain() ?: return null
        val pk = keyStore.getKey(ALIAS, null) as? PrivateKey ?: return null
        return arrayOf(KeystoreKeyManager(pk, chain))
    }

    /** trustManagerForCA builds a trust manager pinned to the given CA PEM. */
    fun trustManagerForCA(caPem: String): X509TrustManager {
        val certs = parseCerts(caPem)
        val ks = KeyStore.getInstance(KeyStore.getDefaultType()).apply { load(null) }
        certs.forEachIndexed { i, c -> ks.setCertificateEntry("ca-$i", c) }
        val tmf = TrustManagerFactory.getInstance(TrustManagerFactory.getDefaultAlgorithm())
        tmf.init(ks)
        return tmf.trustManagers.filterIsInstance<X509TrustManager>().first()
    }

    /** caPem returns the stored CA (last cert in the chain), for the trust manager. */
    fun caPem(): String? {
        val chain = chainFile().takeIf { it.exists() }?.readText() ?: return null
        val certs = parseCerts(chain)
        return certs.lastOrNull()?.let { pemEncode(it) }
    }

    // ── internals ────────────────────────────────────────────────────────────

    private fun generateKeyIfNeeded() {
        if (keyStore.containsAlias(ALIAS)) return
        val kpg = KeyPairGenerator.getInstance(KeyProperties.KEY_ALGORITHM_EC, ANDROID_KEYSTORE)
        // Prefer StrongBox; fall back to TEE if the device lacks it.
        try {
            kpg.initialize(keySpec(strongBox = true))
            kpg.generateKeyPair()
            return
        } catch (_: Exception) {
            // StrongBox unavailable — retry in the TEE.
        }
        val kpg2 = KeyPairGenerator.getInstance(KeyProperties.KEY_ALGORITHM_EC, ANDROID_KEYSTORE)
        kpg2.initialize(keySpec(strongBox = false))
        kpg2.generateKeyPair()
    }

    private fun keySpec(strongBox: Boolean): KeyGenParameterSpec {
        val b = KeyGenParameterSpec.Builder(ALIAS, KeyProperties.PURPOSE_SIGN)
            .setAlgorithmParameterSpec(ECGenParameterSpec("secp256r1"))
            .setDigests(KeyProperties.DIGEST_SHA256)
            .setUserAuthenticationRequired(true)
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) {
            // One biometric unlocks the key for a short session (mTLS handshakes).
            b.setUserAuthenticationParameters(
                AUTH_WINDOW_SECONDS,
                KeyProperties.AUTH_BIOMETRIC_STRONG or KeyProperties.AUTH_DEVICE_CREDENTIAL,
            )
        } else {
            @Suppress("DEPRECATION")
            b.setUserAuthenticationValidityDurationSeconds(AUTH_WINDOW_SECONDS)
        }
        if (strongBox && Build.VERSION.SDK_INT >= Build.VERSION_CODES.P) {
            b.setIsStrongBoxBacked(true)
        }
        return b.build()
    }

    private fun buildCsr(cn: String): String {
        val pk = keyStore.getKey(ALIAS, null) as PrivateKey
        val pub = keyStore.getCertificate(ALIAS).publicKey
        val csr = JcaPKCS10CertificationRequestBuilder(X500Name("CN=$cn"), pub)
            .build(keystoreSigner(pk))
        val sw = StringWriter()
        JcaPEMWriter(sw).use { it.writeObject(csr) }
        return sw.toString()
    }

    // keystoreSigner signs CSR bytes with the Keystore key via java Signature (this
    // is where the biometric gate fires on first use within the auth window).
    private fun keystoreSigner(pk: PrivateKey): ContentSigner {
        val sig = Signature.getInstance("SHA256withECDSA").apply { initSign(pk) }
        val algId = DefaultSignatureAlgorithmIdentifierFinder().find("SHA256WITHECDSA")
        val buf = ByteArrayOutputStream()
        return object : ContentSigner {
            override fun getAlgorithmIdentifier() = algId
            override fun getOutputStream(): OutputStream = object : OutputStream() {
                override fun write(b: Int) = buf.write(b)
                override fun write(b: ByteArray, off: Int, len: Int) = buf.write(b, off, len)
            }
            override fun getSignature(): ByteArray {
                sig.update(buf.toByteArray())
                return sig.sign()
            }
        }
    }

    private fun enrollClient(caPinPem: String?): OkHttpClient {
        if (caPinPem.isNullOrBlank()) return OkHttpClient()
        val tm = trustManagerForCA(caPinPem)
        val ssl = SSLContext.getInstance("TLS").apply { init(null, arrayOf(tm), null) }
        return OkHttpClient.Builder().sslSocketFactory(ssl.socketFactory, tm).build()
    }

    private fun chainFile(): File = File(context.filesDir, CHAIN_FILE)

    private fun readChain(): Array<X509Certificate>? {
        val f = chainFile()
        if (!f.exists()) return null
        val certs = parseCerts(f.readText())
        return if (certs.isEmpty()) null else certs.toTypedArray()
    }

    private fun parseCerts(pem: String): List<X509Certificate> {
        val cf = CertificateFactory.getInstance("X.509")
        return ByteArrayInputStream(pem.toByteArray()).use { ins ->
            cf.generateCertificates(ins).map { it as X509Certificate }
        }
    }

    private fun pemEncode(cert: X509Certificate): String {
        val sw = StringWriter()
        JcaPEMWriter(sw).use { it.writeObject(cert) }
        return sw.toString()
    }

    // KeystoreKeyManager presents the enrolled chain + Keystore-held private key.
    private class KeystoreKeyManager(
        private val key: PrivateKey,
        private val chain: Array<X509Certificate>,
    ) : X509ExtendedKeyManager() {
        override fun getClientAliases(keyType: String?, issuers: Array<out Principal>?) = arrayOf(ALIAS)
        override fun chooseClientAlias(keyType: Array<out String>?, issuers: Array<out Principal>?, socket: Socket?) = ALIAS
        override fun getServerAliases(keyType: String?, issuers: Array<out Principal>?): Array<String>? = null
        override fun chooseServerAlias(keyType: String?, issuers: Array<out Principal>?, socket: Socket?): String? = null
        override fun getCertificateChain(alias: String?): Array<X509Certificate> = chain
        override fun getPrivateKey(alias: String?): PrivateKey = key
    }

    companion object {
        private const val ANDROID_KEYSTORE = "AndroidKeyStore"
        private const val ALIAS = "honey_device_mtls"
        private const val CHAIN_FILE = "device_client_chain.pem"
        private const val AUTH_WINDOW_SECONDS = 300
    }
}
