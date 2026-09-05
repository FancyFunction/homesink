package de.homesink.app.data.net

import kotlinx.coroutines.runBlocking
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import okhttp3.tls.HandshakeCertificates
import okhttp3.tls.HeldCertificate
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import java.security.cert.CertificateException

/**
 * WP-C5 acceptance: "a wrong certificate fails the call (pinning actually
 * engaged — verify with a second self-signed cert)."
 *
 * Two self-signed certificates are generated. The server presents the first; the
 * client is pinned once to the first (must succeed) and once to the second (must
 * fail). Without a real second certificate this test would pass against a
 * client that does no pinning at all, which is the failure mode D-02 cares about.
 */
class PinningTest {

    private val serverCertificate: HeldCertificate = HeldCertificate.Builder()
        .addSubjectAlternativeName(LOCALHOST)
        .commonName("homesink-server")
        .build()

    /** A second, unrelated self-signed cert — the "wrong certificate". */
    private val otherCertificate: HeldCertificate = HeldCertificate.Builder()
        .addSubjectAlternativeName(LOCALHOST)
        .commonName("not-the-homesink-server")
        .build()

    private fun httpsServer(): MockWebServer {
        val handshake = HandshakeCertificates.Builder()
            .heldCertificate(serverCertificate)
            .build()
        return MockWebServer().apply {
            useHttps(handshake.sslSocketFactory(), false)
        }
    }

    @Test
    fun theServerPinFromPairingLetsTheCallThrough() = runBlocking {
        val pin = Pinning.spkiSha256(serverCertificate.certificate)

        NetTestSupport.harness(server = httpsServer(), useTls = true, spkiSha256 = pin).use { h ->
            h.server.enqueue(MockResponse().setResponseCode(200).setBody("ok"))

            val response = h.api.healthz()

            assertEquals(200, response.code())
            assertEquals("ok", response.body()?.string())
        }
    }

    @Test
    fun aWrongCertificateFailsTheCall() = runBlocking {
        // The pin of a certificate the server does not hold.
        val wrongPin = Pinning.spkiSha256(otherCertificate.certificate)
        assertNotEquals(Pinning.spkiSha256(serverCertificate.certificate), wrongPin)

        NetTestSupport.harness(server = httpsServer(), useTls = true, spkiSha256 = wrongPin).use { h ->
            h.server.enqueue(MockResponse().setResponseCode(200).setBody("ok"))

            val error = h.client.getSystemStatus().errorOrNull()

            assertTrue(
                "a pin mismatch must fail the call, not fall through: $error",
                error is ApiError.Transport,
            )
        }
    }

    @Test
    fun anUnpinnedClientDoesNotTrustTheSelfSignedServer() = runBlocking {
        // Sanity check on the test itself: without a pin the platform trust
        // manager rejects the self-signed chain, so the success case above
        // proves the pin did the trusting rather than a lenient default.
        NetTestSupport.harness(server = httpsServer(), useTls = true, spkiSha256 = null).use { h ->
            h.server.enqueue(MockResponse().setResponseCode(200).setBody("ok"))

            val error = h.client.getSystemStatus().errorOrNull()

            assertTrue("$error", error is ApiError.Transport)
        }
    }

    @Test
    fun spkiTrustManagerRejectsAnEmptyChainAndAnUnpinnedLeaf() {
        val trustManager = SpkiTrustManager(Pinning.spkiSha256(serverCertificate.certificate))

        trustManager.checkServerTrusted(arrayOf(serverCertificate.certificate), "EC")

        assertTrue(
            "no issuer is trusted, only the pinned leaf key",
            trustManager.acceptedIssuers.isEmpty(),
        )
        assertThrowsCertificateException { trustManager.checkServerTrusted(emptyArray(), "EC") }
        assertThrowsCertificateException { trustManager.checkServerTrusted(null, "EC") }
        assertThrowsCertificateException {
            trustManager.checkServerTrusted(arrayOf(otherCertificate.certificate), "EC")
        }
        assertThrowsCertificateException {
            trustManager.checkClientTrusted(arrayOf(serverCertificate.certificate), "EC")
        }
    }

    @Test
    fun hostnameVerificationFollowsTheKeyNotTheName() = runBlocking {
        // D-02: a DHCP lease change moves the server to an address the cert's
        // SANs never covered; the pairing must survive that.
        val pin = Pinning.spkiSha256(serverCertificate.certificate)
        val verifier = SpkiHostnameVerifier(pin)

        NetTestSupport.harness(server = httpsServer(), useTls = true, spkiSha256 = pin).use { h ->
            h.server.enqueue(MockResponse().setResponseCode(200).setBody("ok"))
            h.api.healthz().body()?.close()
        }

        // The verifier answers on the key alone, so an unrelated hostname with no
        // session is refused rather than waved through.
        assertEquals(false, verifier.verify("192.168.1.77", null))
    }

    @Test
    fun theSpkiPinIsTheBase64Sha256OfTheSubjectPublicKeyInfo() {
        val expected = java.util.Base64.getEncoder().encodeToString(
            java.security.MessageDigest.getInstance("SHA-256")
                .digest(serverCertificate.certificate.publicKey.encoded),
        )

        assertEquals(expected, Pinning.spkiSha256(serverCertificate.certificate))
        // Two different keys must never produce the same pin.
        assertNotEquals(expected, Pinning.spkiSha256(otherCertificate.certificate))
    }

    private fun assertThrowsCertificateException(block: () -> Unit) {
        try {
            block()
            throw AssertionError("expected a CertificateException")
        } catch (expected: CertificateException) {
            assertTrue(expected.message.orEmpty().isNotEmpty())
        }
    }

    private companion object {
        const val LOCALHOST = "localhost"
    }
}
