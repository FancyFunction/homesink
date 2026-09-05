package de.homesink.app.data.net

import kotlinx.coroutines.runBlocking
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.mockwebserver.MockResponse
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * The interceptor chain WP-C5 specifies, other than auth (see
 * [AuthInterceptorTest]) and pinning (see [PinningTest]): the client-version
 * stamp, the `X-Homesink-Latest` parser behind the [UpdateAvailability] flow,
 * the per-request address rewrite, and the OkHttp configuration the brief calls
 * for — HTTP/2, a 30 s call timeout with none on transfers, and a connection
 * pool that outlives a sync run.
 */
class InterceptorsTest {

    // ------------------------------------------------------------ X-Homesink-Client

    @Test
    fun everyRequestCarriesTheClientVersionHeader() = runBlocking {
        NetTestSupport.harness().use { h ->
            h.server.enqueue(MockResponse().setResponseCode(200).setBody("[]"))
            h.server.enqueue(MockResponse().setResponseCode(200).setBody("[]"))

            h.client.listAlbums()
            h.client.listDevices()

            // D-33: the version rides along on every request, which is what makes
            // the update check cost zero extra round trips.
            repeat(2) {
                assertEquals("14/1.4.0", h.server.takeRequest().getHeader(HomesinkHeaders.CLIENT))
            }
        }
    }

    @Test
    fun theClientVersionHeaderIsCodeSlashName() {
        assertEquals("15/1.4.0", ClientVersion(15, "1.4.0").headerValue)
    }

    // ------------------------------------------------------------ X-Homesink-Latest

    @Test
    fun theLatestHeaderOnAnyResponsePublishesToTheUpdateAvailabilityFlow() = runBlocking {
        NetTestSupport.harness().use { h ->
            assertNull("nothing is advertised until the server says so", h.updateAvailability.latest.value)

            h.server.enqueue(
                MockResponse().setResponseCode(200).setBody("[]")
                    .addHeader(HomesinkHeaders.LATEST, LATEST_HEADER),
            )

            h.client.listAlbums()

            assertEquals(
                UpdateInfo(15, "1.4.0", SHA256, DOWNLOAD_URL),
                h.updateAvailability.latest.value,
            )
        }
    }

    @Test
    fun theLatestHeaderIsAlsoReadOffAnErrorResponse() = runBlocking {
        NetTestSupport.harness().use { h ->
            h.server.enqueue(
                MockResponse().setResponseCode(500).setBody("")
                    .addHeader(HomesinkHeaders.LATEST, LATEST_HEADER),
            )

            h.client.listAlbums()

            // D-33 says "any response"; a failing call still tells us an update exists.
            assertEquals(15, h.updateAvailability.latest.value?.versionCode)
        }
    }

    @Test
    fun aMalformedLatestHeaderIsIgnoredRatherThanFailingTheCall() = runBlocking {
        NetTestSupport.harness().use { h ->
            h.server.enqueue(
                MockResponse().setResponseCode(200).setBody("[]")
                    .addHeader(HomesinkHeaders.LATEST, "not;a;version"),
            )

            val result = h.client.listAlbums()

            assertTrue("an update hint must never fail the call it rode on", result is ApiResult.Success)
            assertNull(h.updateAvailability.latest.value)
        }
    }

    @Test
    fun latestHeaderParsingRejectsEveryIncompleteShape() {
        assertEquals(
            UpdateInfo(15, "1.4.0", SHA256, DOWNLOAD_URL),
            LatestVersionInterceptor.parse(LATEST_HEADER),
        )
        assertNull(LatestVersionInterceptor.parse(""))
        assertNull(LatestVersionInterceptor.parse("15;1.4.0;$SHA256"))
        assertNull(LatestVersionInterceptor.parse("15;1.4.0;$SHA256;$DOWNLOAD_URL;extra"))
        assertNull(LatestVersionInterceptor.parse("x;1.4.0;$SHA256;$DOWNLOAD_URL"))
        assertNull(LatestVersionInterceptor.parse("15;;$SHA256;$DOWNLOAD_URL"))
        assertNull(LatestVersionInterceptor.parse("15;1.4.0;;$DOWNLOAD_URL"))
        assertNull(LatestVersionInterceptor.parse("15;1.4.0;$SHA256;"))
    }

    @Test
    fun anOlderAdvertisedVersionNeverReplacesANewerOne() {
        val availability = UpdateAvailability()

        availability.publish(UpdateInfo(15, "1.4.0", SHA256, DOWNLOAD_URL))
        availability.publish(UpdateInfo(14, "1.3.0", SHA256, DOWNLOAD_URL))

        // D-34: versionCode decides, versionName is cosmetic.
        assertEquals(15, availability.latest.value?.versionCode)

        availability.publish(UpdateInfo(16, "1.5.0", SHA256, DOWNLOAD_URL))
        assertEquals(16, availability.latest.value?.versionCode)
    }

    // ------------------------------------------------------------ server address

    @Test
    fun requestsAreRewrittenOntoTheCurrentServerAddress() = runBlocking {
        NetTestSupport.harness().use { h ->
            h.server.enqueue(MockResponse().setResponseCode(200).setBody("[]"))

            h.client.listAlbums()

            val sent = h.server.takeRequest()
            assertEquals("/v1/library/albums", sent.path)
            assertFalse(
                "the placeholder authority must never reach the wire",
                sent.getHeader("Host").orEmpty().contains("homesink.invalid"),
            )
        }
    }

    @Test
    fun aCallBeforePairingIsRefusedWithoutTouchingTheNetwork() = runBlocking {
        NetTestSupport.harness().use { h ->
            h.addressSource.clear()

            val error = h.client.listAlbums().errorOrNull()

            assertEquals(ApiError.NotConfigured, error)
            assertEquals("no request may be attempted", 0, h.server.requestCount)
        }
    }

    @Test
    fun anAddressChangeTakesEffectWithoutRebuildingRetrofit() = runBlocking {
        // D-03: the router reassigns the sink's IP and mDNS re-resolves it; the
        // singleton Retrofit must follow without being rebuilt.
        NetTestSupport.harness().use { first ->
            okhttp3.mockwebserver.MockWebServer().use { second ->
                second.start()
                first.server.enqueue(MockResponse().setResponseCode(200).setBody("[]"))
                second.enqueue(MockResponse().setResponseCode(200).setBody("[]"))

                first.client.listAlbums()
                assertEquals(1, first.server.requestCount)

                first.addressSource.set(ServerAddress(second.hostName, second.port, useTls = false))
                first.client.listAlbums()

                assertEquals(1, second.requestCount)
            }
        }
    }

    // ------------------------------------------------------------ OkHttp configuration

    @Test
    fun theConnectionPoolIsReusedAcrossACompleteRunOfCalls() = runBlocking {
        NetTestSupport.harness().use { h ->
            repeat(CALLS_IN_A_RUN) { h.server.enqueue(MockResponse().setResponseCode(200).setBody("[]")) }

            repeat(CALLS_IN_A_RUN) { h.client.listAlbums() }

            // sequenceNumber > 0 means the socket was reused rather than redialled,
            // which is the connection pool surviving the run (WP-C5).
            h.server.takeRequest()
            for (i in 1 until CALLS_IN_A_RUN) {
                assertEquals(
                    "call $i should reuse the pooled connection",
                    i.toLong(),
                    h.server.takeRequest().sequenceNumber.toLong(),
                )
            }
        }
    }

    @Test
    fun transferCallsAreTheOnesThatMustNotCarryTheCallTimeout() {
        // Streams bytes: the chunk PUT, the chunk GET, media, the APK.
        assertTrue(PinnedCallFactory.isTransferCall(request("PUT", "/v1/blobs/$HASH")))
        assertTrue(PinnedCallFactory.isTransferCall(request("GET", "/v1/blobs/$HASH")))
        assertTrue(PinnedCallFactory.isTransferCall(request("GET", "/v1/media/itm_9a8b7c6d")))
        assertTrue(PinnedCallFactory.isTransferCall(request("GET", "/v1/app/download")))

        // Small JSON or header-only: the 30 s call timeout applies.
        assertFalse(PinnedCallFactory.isTransferCall(request("HEAD", "/v1/blobs/$HASH")))
        assertFalse(PinnedCallFactory.isTransferCall(request("DELETE", "/v1/blobs/$HASH")))
        assertFalse(PinnedCallFactory.isTransferCall(request("POST", "/v1/blobs/$HASH/commit")))
        assertFalse(PinnedCallFactory.isTransferCall(request("POST", "/v1/preflight")))
        assertFalse(PinnedCallFactory.isTransferCall(request("GET", "/v1/library/items")))
        assertFalse(PinnedCallFactory.isTransferCall(request("GET", "/v1/app/latest")))
        assertFalse(PinnedCallFactory.isTransferCall(request("GET", "/healthz")))
    }

    @Test
    fun theBaseClientPrefersHttp2AndTimesOutNonTransferCallsAfter30s() {
        val addressSource = MutableServerAddressSource()
        val client = de.homesink.app.di.NetworkModule.provideOkHttpClient(
            connectionPool = de.homesink.app.di.NetworkModule.provideConnectionPool(),
            serverAddress = ServerAddressInterceptor(addressSource),
            clientVersion = ClientVersionInterceptor(NetTestSupport.TEST_CLIENT_VERSION),
            auth = AuthInterceptor(InMemoryTokenStore()),
            latest = LatestVersionInterceptor(UpdateAvailability()),
        )

        // D-02: HTTP/2 multiplexing is the biggest single win for many small GETs.
        assertEquals(okhttp3.Protocol.HTTP_2, client.protocols.first())
        assertTrue(client.protocols.contains(okhttp3.Protocol.HTTP_1_1))
        assertEquals(THIRTY_SECONDS_MS, client.callTimeoutMillis)

        // Address rewrite, client version, auth, latest-parser (Interceptors.kt).
        assertEquals(4, client.interceptors.size)
    }

    @Test
    fun transferCallsCarryNoCallReadOrWriteTimeoutWhileEverythingElseDoes() {
        val addressSource = MutableServerAddressSource().apply {
            set(ServerAddress("192.168.1.20", 8443, useTls = false))
        }
        val factory = PinnedCallFactory(
            de.homesink.app.di.NetworkModule.provideOkHttpClient(
                connectionPool = de.homesink.app.di.NetworkModule.provideConnectionPool(),
                serverAddress = ServerAddressInterceptor(addressSource),
                clientVersion = ClientVersionInterceptor(NetTestSupport.TEST_CLIENT_VERSION),
                auth = AuthInterceptor(InMemoryTokenStore()),
                latest = LatestVersionInterceptor(UpdateAvailability()),
            ),
            addressSource,
        )

        val transfer = factory.clientFor(request("PUT", "/v1/blobs/$HASH"))
        assertEquals("an 8 MiB chunk on a weak link may take longer than 30 s", 0, transfer.callTimeoutMillis)
        assertEquals(0, transfer.readTimeoutMillis)
        assertEquals(0, transfer.writeTimeoutMillis)

        val standard = factory.clientFor(request("POST", "/v1/preflight"))
        assertEquals(THIRTY_SECONDS_MS, standard.callTimeoutMillis)

        // Both are derived from the same base, so the pool survives the whole run.
        assertTrue(transfer.connectionPool === standard.connectionPool)
    }

    private fun request(method: String, path: String): Request = Request.Builder()
        .url("https://homesink.invalid$path")
        .method(method, if (method == "PUT" || method == "POST") EMPTY_BODY else null)
        .build()

    private companion object {
        const val HASH = "5466fae56722d061d4d685bfee7b310f523ca684544cbf2cf03d9fffa53d810b"
        const val SHA256 = "a059a2178a60626620de4d66c1201e16a992e247b25af7ca092ba834ebcfe44c"
        const val DOWNLOAD_URL = "https://homesink.local:8443/v1/app/download"
        const val LATEST_HEADER = "15;1.4.0;$SHA256;$DOWNLOAD_URL"
        const val CALLS_IN_A_RUN = 5
        const val THIRTY_SECONDS_MS = 30_000

        val EMPTY_BODY: okhttp3.RequestBody =
            ByteArray(0).toRequestBody("application/octet-stream".toMediaType())
    }
}
