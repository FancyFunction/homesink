package de.homesink.app.data.net

import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.runBlocking
import okhttp3.mockwebserver.MockResponse
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.util.concurrent.atomic.AtomicInteger

/**
 * WP-C5 acceptance: "a 401 clears the token exactly once."
 *
 * The count matters because a sync run has three files in flight (D-17): a
 * revoked device collects three simultaneous 401s, and three clears would race
 * WP-C6's re-pairing and wipe a token that had just been stored.
 */
class AuthInterceptorTest {

    /** A [TokenStore] that records how often the token was dropped. */
    private class CountingTokenStore(initial: String?) : TokenStore {
        private var current: String? = initial
        val clears = AtomicInteger(0)

        override fun token(): String? = current

        override fun clear() {
            clears.incrementAndGet()
            current = null
        }

        fun set(token: String) {
            current = token
        }
    }

    @Test
    fun aSingle401ClearsTheTokenExactlyOnce() = runBlocking {
        val tokens = CountingTokenStore(TOKEN)
        val interceptor = AuthInterceptor(tokens)

        NetTestSupport.harnessWith(interceptor).use { h ->
            h.server.enqueue(MockResponse().setResponseCode(401))

            val error = h.client.listAlbums().errorOrNull()

            assertTrue("$error", error is ApiError.Unauthorized)
            assertEquals(1, tokens.clears.get())
            assertNull(tokens.token())
        }
    }

    @Test
    fun repeated401sForTheSameTokenClearItOnlyOnce() = runBlocking {
        val tokens = CountingTokenStore(TOKEN)

        NetTestSupport.harnessWith(AuthInterceptor(tokens)).use { h ->
            repeat(3) { h.server.enqueue(MockResponse().setResponseCode(401)) }

            repeat(3) { h.client.listAlbums() }

            assertEquals("three 401s, one token drop", 1, tokens.clears.get())
        }
    }

    @Test
    fun concurrent401sFromFilesInFlightClearTheTokenOnlyOnce() = runBlocking {
        val tokens = CountingTokenStore(TOKEN)

        NetTestSupport.harnessWith(AuthInterceptor(tokens)).use { h ->
            repeat(CONCURRENT_UPLOADS) { h.server.enqueue(MockResponse().setResponseCode(401)) }

            (1..CONCURRENT_UPLOADS).map { async { h.client.listAlbums() } }.awaitAll()

            assertEquals(1, tokens.clears.get())
        }
    }

    @Test
    fun aFreshTokenAfterRePairingReArmsTheOneShotClear() = runBlocking {
        val tokens = CountingTokenStore(TOKEN)

        NetTestSupport.harnessWith(AuthInterceptor(tokens)).use { h ->
            h.server.enqueue(MockResponse().setResponseCode(401))
            h.client.listAlbums()
            assertEquals(1, tokens.clears.get())

            // WP-C6 re-pairs and stores a new token; that token is a new subject
            // and must be droppable in turn if the server rejects it too.
            tokens.set(OTHER_TOKEN)
            h.server.enqueue(MockResponse().setResponseCode(401))
            h.client.listAlbums()

            assertEquals(2, tokens.clears.get())
        }
    }

    @Test
    fun theBearerTokenIsSentOnEveryRequestAndOmittedWhenUnpaired() = runBlocking {
        val tokens = CountingTokenStore(TOKEN)

        NetTestSupport.harnessWith(AuthInterceptor(tokens)).use { h ->
            h.server.enqueue(MockResponse().setResponseCode(200).setBody("[]"))
            h.client.listAlbums()
            assertEquals("Bearer $TOKEN", h.server.takeRequest().getHeader(HomesinkHeaders.AUTHORIZATION))

            tokens.clear()
            h.server.enqueue(MockResponse().setResponseCode(200).setBody("[]"))
            h.client.listAlbums()
            assertNull(
                "an unpaired device sends no Authorization header",
                h.server.takeRequest().getHeader(HomesinkHeaders.AUTHORIZATION),
            )
        }
    }

    @Test
    fun aSuccessfulResponseNeverClearsTheToken() = runBlocking {
        val tokens = CountingTokenStore(TOKEN)

        NetTestSupport.harnessWith(AuthInterceptor(tokens)).use { h ->
            h.server.enqueue(MockResponse().setResponseCode(200).setBody("[]"))
            h.server.enqueue(MockResponse().setResponseCode(500).setBody(""))

            h.client.listAlbums()
            h.client.listAlbums()

            assertEquals(0, tokens.clears.get())
            assertEquals(TOKEN, tokens.token())
        }
    }

    private companion object {
        const val TOKEN = "hs_kJ8f2Nq4Xr7wZ1aB3cD5eF6gH9iJ0kL2mN4oP6qR8sT"
        const val OTHER_TOKEN = "hs_ZZZf2Nq4Xr7wZ1aB3cD5eF6gH9iJ0kL2mN4oP6qR8sT"

        /** Files in flight during a sync run (D-17). */
        const val CONCURRENT_UPLOADS = 3
    }
}
