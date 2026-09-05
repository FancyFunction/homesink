package de.homesink.app.data.net

import de.homesink.app.di.NetworkModule
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject
import okhttp3.mockwebserver.MockWebServer
import java.io.Closeable

/**
 * Shared plumbing for the WP-C5 test suite.
 *
 * [harness] builds the client through `NetworkModule`'s own provider functions,
 * so the tests exercise the wiring that ships rather than a parallel copy of it.
 */
object NetTestSupport {

    /** The version this test app claims to be, for `X-Homesink-Client` assertions. */
    val TEST_CLIENT_VERSION = ClientVersion(versionCode = 14, versionName = "1.4.0")

    class Harness(
        val server: MockWebServer,
        val json: Json,
        val api: HomesinkApi,
        val client: HomesinkClient,
        val tokenStore: InMemoryTokenStore,
        val addressSource: MutableServerAddressSource,
        val updateAvailability: UpdateAvailability,
    ) : Closeable {
        override fun close() {
            server.shutdown()
        }
    }

    /**
     * A live [MockWebServer] plus a client pointed at it.
     *
     * @param server pass a pre-configured server (e.g. one already switched to
     *   HTTPS) when the test needs it; otherwise a plain HTTP one is started.
     * @param spkiSha256 the pin to enforce; `null` leaves TLS unpinned.
     */
    fun harness(
        server: MockWebServer = MockWebServer(),
        useTls: Boolean = false,
        spkiSha256: String? = null,
        clientVersion: ClientVersion = TEST_CLIENT_VERSION,
        tokenStore: InMemoryTokenStore = InMemoryTokenStore(),
        auth: AuthInterceptor = AuthInterceptor(tokenStore),
    ): Harness {
        server.start()

        val addressSource = MutableServerAddressSource().apply {
            set(
                ServerAddress(
                    host = server.hostName,
                    port = server.port,
                    useTls = useTls,
                    spkiSha256 = spkiSha256,
                ),
            )
        }
        val updateAvailability = UpdateAvailability()

        val json = NetworkModule.provideJson()
        val base = NetworkModule.provideOkHttpClient(
            connectionPool = NetworkModule.provideConnectionPool(),
            serverAddress = ServerAddressInterceptor(addressSource),
            clientVersion = ClientVersionInterceptor(clientVersion),
            auth = auth,
            latest = LatestVersionInterceptor(updateAvailability),
        )
        val retrofit = NetworkModule.provideRetrofit(
            callFactory = PinnedCallFactory(base, addressSource),
            json = json,
        )
        val api = NetworkModule.provideHomesinkApi(retrofit)

        return Harness(
            server = server,
            json = json,
            api = api,
            client = HomesinkClient(api, json),
            tokenStore = tokenStore,
            addressSource = addressSource,
            updateAvailability = updateAvailability,
        )
    }

    /** A harness whose auth interceptor is supplied by the test, so it can count token drops. */
    fun harnessWith(auth: AuthInterceptor): Harness = harness(auth = auth)

    /** Reads one file from the shared fixture directory copied in by `syncFixtures`. */
    fun fixture(name: String): String =
        NetTestSupport::class.java.classLoader
            ?.getResourceAsStream("fixtures/$name")
            ?.use { it.readBytes().decodeToString() }
            ?: error(
                "fixture 'fixtures/$name' is missing - run scripts/sync-fixtures.sh " +
                    "(08-ROADMAP.md §5: backend/internal/testutil/testdata is the source of truth)",
            )

    /**
     * Asserts that re-encoding a DTO kept every field the fixture carried, with
     * the same value. A renamed or dropped field on either side fails here,
     * which is the whole point of sharing the bytes with the Go suite.
     */
    fun assertNoFieldLost(path: String, original: JsonElement, reencoded: JsonElement) {
        when (original) {
            is JsonObject -> {
                val actual = reencoded as? JsonObject
                    ?: error("$path: expected an object, got $reencoded")
                for ((key, value) in original) {
                    val mirrored = actual[key]
                        ?: error("$path.$key: present in the shared fixture, absent after round-trip")
                    assertNoFieldLost("$path.$key", value, mirrored)
                }
            }
            is kotlinx.serialization.json.JsonArray -> {
                val actual = reencoded as? kotlinx.serialization.json.JsonArray
                    ?: error("$path: expected an array, got $reencoded")
                check(actual.size == original.size) {
                    "$path: expected ${original.size} elements, got ${actual.size}"
                }
                original.forEachIndexed { index, element ->
                    assertNoFieldLost("$path[$index]", element, actual[index])
                }
            }
            else -> check(original == reencoded) {
                "$path: expected $original, got $reencoded"
            }
        }
    }
}
