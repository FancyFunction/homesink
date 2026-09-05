package de.homesink.app.data.net

import okhttp3.Call
import okhttp3.OkHttpClient
import okhttp3.Request
import java.security.MessageDigest
import java.security.cert.CertificateException
import java.security.cert.X509Certificate
import java.util.Base64
import java.util.concurrent.TimeUnit
import javax.inject.Inject
import javax.inject.Singleton
import javax.net.ssl.HostnameVerifier
import javax.net.ssl.SSLContext
import javax.net.ssl.SSLSession
import javax.net.ssl.TrustManager
import javax.net.ssl.X509TrustManager

/**
 * TLS pinning against the server's self-signed certificate (D-02), and the
 * [Call.Factory] that applies it (WP-C5).
 *
 * The server generates one EC P-256 self-signed cert on first run and hands the
 * base64 SHA-256 of its SPKI to the client in the pairing response. From then on
 * that key — and nothing else — is trusted.
 *
 * **Why the pin is a trust manager rather than OkHttp's `CertificatePinner`.**
 * D-02 names `CertificatePinner`, but it cannot be the mechanism for a
 * self-signed server. `CertificatePinner` runs *after* the platform trust
 * manager has accepted the chain, and it pins the chain OkHttp's chain cleaner
 * produces — which requires a trusted anchor that a self-signed cert, by
 * definition, does not have. Installing both makes every call fail with
 * `SSLPeerUnverifiedException` before the pin is ever compared.
 *
 * [SpkiTrustManager] implements exactly the check D-02 specifies — SHA-256 of
 * the leaf's SubjectPublicKeyInfo must equal the pin from pairing — as the trust
 * decision itself. It is **not** `trustAllCerts`: with no pin nothing is
 * accepted, and with a pin exactly one public key is, no CA included.
 *
 * [SpkiHostnameVerifier] defers to the same pin, because D-02 pins the key
 * rather than the name: when the router reassigns the server's IP the cert's
 * SANs no longer match, and the pairing must survive that.
 */
object Pinning {

    /** base64 SHA-256 of a certificate's SubjectPublicKeyInfo — the `tlsSpkiSha256` of the pairing response. */
    fun spkiSha256(certificate: X509Certificate): String {
        val digest = MessageDigest.getInstance("SHA-256").digest(certificate.publicKey.encoded)
        return Base64.getEncoder().encodeToString(digest)
    }

    /** Applies the pin to [builder]: pin-only trust and the matching hostname verifier. */
    fun applyTo(builder: OkHttpClient.Builder, spkiSha256: String): OkHttpClient.Builder {
        val trustManager = SpkiTrustManager(spkiSha256)
        val sslContext = SSLContext.getInstance("TLS").apply {
            init(null, arrayOf<TrustManager>(trustManager), null)
        }
        return builder
            .sslSocketFactory(sslContext.socketFactory, trustManager)
            .hostnameVerifier(SpkiHostnameVerifier(spkiSha256))
    }
}

/**
 * Trusts exactly one public key: the leaf's SPKI SHA-256 must equal [spkiSha256].
 * Every other chain — a real CA's included — is rejected.
 */
@Suppress("CustomX509TrustManager")
// Lint's warning is that a custom trust manager usually disables validation.
// Here it is the validation: D-02 requires trusting exactly one self-signed
// public key and no CA, which the platform implementation cannot express.
// `checkServerTrusted` compares the leaf's SPKI against the pin and throws on
// any mismatch, an empty chain, or a null chain - PinningTest covers each of
// those paths, and proves the pin engages by pointing a second self-signed
// certificate at the same server.
class SpkiTrustManager(private val spkiSha256: String) : X509TrustManager {

    override fun checkClientTrusted(chain: Array<out X509Certificate>?, authType: String?) {
        throw CertificateException("client certificates are not used")
    }

    override fun checkServerTrusted(chain: Array<out X509Certificate>?, authType: String?) {
        val leaf = chain?.firstOrNull()
            ?: throw CertificateException("empty certificate chain")
        val presented = Pinning.spkiSha256(leaf)
        if (!MessageDigest.isEqual(presented.toByteArray(), spkiSha256.toByteArray())) {
            throw CertificateException("certificate SPKI does not match the pinned key")
        }
    }

    /** Empty on purpose: no issuer is trusted, only the pinned leaf key. */
    override fun getAcceptedIssuers(): Array<X509Certificate> = emptyArray()
}

/**
 * Accepts any hostname whose peer presents the pinned key. D-02: "If the
 * server's IP changes the cert stays valid (pinning is on the key, not the
 * name)" — the key, already verified by [SpkiTrustManager], is the identity.
 */
class SpkiHostnameVerifier(private val spkiSha256: String) : HostnameVerifier {

    override fun verify(hostname: String?, session: SSLSession?): Boolean {
        val leaf = session?.peerCertificates?.firstOrNull() as? X509Certificate ?: return false
        return MessageDigest.isEqual(Pinning.spkiSha256(leaf).toByteArray(), spkiSha256.toByteArray())
    }
}

/**
 * Retrofit's [Call.Factory], choosing the right OkHttp client per request.
 *
 * Two axes, both from WP-C5's brief:
 *  - **Pinning.** The pin arrives at pairing time and can change (re-pairing, a
 *    rebuilt server), so the pinned client is derived lazily from [base] and
 *    cached until the address changes. `newBuilder()` shares [base]'s connection
 *    pool and dispatcher, so "a connection pool that survives the whole sync
 *    run" holds across a re-derive.
 *  - **Timeouts.** A 30 s call timeout everywhere, except the transfer calls
 *    ([isTransferCall]) which stream gigabytes and must have no call, read or
 *    write timeout at all.
 */
@Singleton
class PinnedCallFactory @Inject constructor(
    private val base: OkHttpClient,
    private val addressSource: ServerAddressSource,
) : Call.Factory {

    private data class Clients(
        val address: ServerAddress?,
        val standard: OkHttpClient,
        val transfer: OkHttpClient,
    )

    @Volatile
    private var cached: Clients? = null

    override fun newCall(request: Request): Call = clientFor(request).newCall(request)

    /** The client this factory would use for [request]: pinned, and timeout-free for transfers. */
    internal fun clientFor(request: Request): OkHttpClient {
        val clients = clientsFor(addressSource.address())
        return if (isTransferCall(request)) clients.transfer else clients.standard
    }

    @Synchronized
    private fun clientsFor(address: ServerAddress?): Clients {
        cached?.let { if (it.address == address) return it }

        val standardBuilder = base.newBuilder()
        if (address != null && address.useTls && address.spkiSha256 != null) {
            Pinning.applyTo(standardBuilder, address.spkiSha256)
        }
        val standard = standardBuilder.build()
        val transfer = standard.newBuilder()
            .callTimeout(NO_TIMEOUT, TimeUnit.MILLISECONDS)
            .readTimeout(NO_TIMEOUT, TimeUnit.MILLISECONDS)
            .writeTimeout(NO_TIMEOUT, TimeUnit.MILLISECONDS)
            .build()

        return Clients(address, standard, transfer).also { cached = it }
    }

    companion object {
        /** OkHttp reads 0 as "no timeout". */
        private const val NO_TIMEOUT = 0L

        /**
         * Whether a request streams bytes rather than JSON: blob chunks up, and
         * media or the APK down. These are the calls that must not carry the
         * 30 s call timeout — an 8 MiB chunk on a weak link, or a 4 GB video
         * download, legitimately takes longer.
         */
        fun isTransferCall(request: Request): Boolean {
            val segments = request.url.pathSegments
            return when {
                segments.size >= 2 && segments[0] == "v1" && segments[1] == "blobs" ->
                    // The commit call is small JSON; only the chunk PUT/GET streams.
                    segments.size < 4 && request.method != "HEAD" && request.method != "DELETE"
                segments.size >= 2 && segments[0] == "v1" && segments[1] == "media" -> true
                segments.size >= 3 && segments[0] == "v1" && segments[1] == "app" &&
                    segments[2] == "download" -> true
                else -> false
            }
        }
    }
}
