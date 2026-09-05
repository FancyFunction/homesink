package de.homesink.app.data.net

import kotlinx.coroutines.runBlocking
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import okhttp3.mockwebserver.MockResponse
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * WP-C5 acceptance: "every error code in `02-API.md §2` maps to a distinct
 * `ApiError`."
 *
 * "Distinct" is asserted structurally — thirteen wire codes must produce
 * thirteen different Kotlin classes — rather than by inspection, so collapsing
 * two codes onto one subtype fails here instead of silently costing the sync
 * engine its retry decision (D-19).
 */
class ApiErrorMappingTest {

    @Test
    fun everyErrorCodeMapsToADistinctApiError() {
        val mapped = ApiError.Codes.WIRE.associateWith { code ->
            ApiError.fromEnvelope(statusFor(code), envelope(code))
        }

        assertEquals(
            "the mapping must cover every code in 02-API.md §2",
            ApiError.Codes.WIRE.size,
            mapped.size,
        )

        // 1. No code falls through to the catch-all.
        val unmapped = mapped.filterValues { it is ApiError.Unexpected }.keys
        assertEquals("codes that fell through to Unexpected:", emptySet<String>(), unmapped)

        // 2. Each code produces a different subtype.
        val classes = mapped.mapValues { it.value.javaClass }
        assertEquals(
            "two codes share one ApiError subtype: " +
                classes.entries.groupBy { it.value }.filterValues { it.size > 1 },
            ApiError.Codes.WIRE.size,
            classes.values.toSet().size,
        )

        // 3. Each mapped error reports the code it came from.
        for ((code, error) in mapped) {
            assertEquals(code, error.code)
        }
    }

    @Test
    fun everyErrorCarriesTheServersGermanMessageAndRetryClass() {
        for (code in ApiError.Codes.WIRE) {
            val error = ApiError.fromEnvelope(statusFor(code), envelope(code, retryable = true))
            // D-37: the message is already localised server-side; the client shows it verbatim.
            assertEquals("Nachricht für $code", error.serverMessage)
            assertTrue("$code should honour the server's retry class", error.retryable)
            assertFalse(
                "$code should honour the server's retry class",
                ApiError.fromEnvelope(statusFor(code), envelope(code, retryable = false)).retryable,
            )
        }
    }

    @Test
    fun errorDetailsAreLiftedOntoTheSubtypeThatNeedsThem() {
        // The four codes whose `details` the sync engine acts on (D-18, D-19, D-20).
        val rangeMismatch = ApiError.fromEnvelope(
            409,
            envelope(
                ApiError.Codes.RANGE_MISMATCH,
                details = mapOf("expectedStart" to 16_777_216L, "receivedStart" to 25_165_824L),
            ),
        )
        assertEquals(16_777_216L, (rangeMismatch as ApiError.RangeMismatch).expectedStart)
        assertEquals(25_165_824L, rangeMismatch.receivedStart)

        val hashMismatch = ApiError.fromEnvelope(
            409,
            envelope(ApiError.Codes.HASH_MISMATCH, details = mapOf("expected" to "aa", "actual" to "bb")),
        )
        assertEquals("aa", (hashMismatch as ApiError.HashMismatch).expected)
        assertEquals("bb", hashMismatch.actual)

        val tooLarge = ApiError.fromEnvelope(
            413,
            envelope(ApiError.Codes.FILE_TOO_LARGE, details = mapOf("maxBytes" to 17_179_869_184L)),
        )
        assertEquals(17_179_869_184L, (tooLarge as ApiError.FileTooLarge).maxBytes)

        val storage = ApiError.fromEnvelope(
            507,
            envelope(
                ApiError.Codes.INSUFFICIENT_STORAGE,
                details = mapOf("freeBytes" to 1_048_576L, "requiredBytes" to 10_737_418_240L),
            ),
        )
        assertEquals(1_048_576L, (storage as ApiError.InsufficientStorage).freeBytes)
        assertEquals(10_737_418_240L, storage.requiredBytes)

        // Absent details must not throw; the subtype simply carries nulls.
        val bare = ApiError.fromEnvelope(409, envelope(ApiError.Codes.RANGE_MISMATCH))
        assertEquals(null, (bare as ApiError.RangeMismatch).expectedStart)
    }

    @Test
    fun anUnknownCodeFromANewerServerBecomesUnexpectedRatherThanACrash() {
        // 02-API.md §7.2: an old app must not break against a new server.
        val error = ApiError.fromEnvelope(418, envelope("SOMETHING_NEW"))

        assertTrue("$error", error is ApiError.Unexpected)
        assertEquals("SOMETHING_NEW", error.code)
        assertEquals(418, (error as ApiError.Unexpected).httpStatus)
    }

    @Test
    fun aNonJsonErrorBodyDegradesToTheStatusAlone() = runBlocking {
        NetTestSupport.harness().use { h ->
            h.server.enqueue(
                MockResponse().setResponseCode(502)
                    .setHeader("Content-Type", "text/html")
                    .setBody("<html>bad gateway</html>"),
            )

            val error = h.client.getSystemStatus().errorOrNull()

            assertTrue("$error", error is ApiError.Unexpected)
            assertEquals(502, (error as ApiError.Unexpected).httpStatus)
            assertTrue("a 5xx stays retryable per D-19", error.retryable)
        }
    }

    @Test
    fun anEmptyErrorBodyOn401StillMapsToUnauthorized() = runBlocking {
        NetTestSupport.harness().use { h ->
            h.tokenStore.set("hs_token")
            h.server.enqueue(MockResponse().setResponseCode(401))

            val error = h.client.listAlbums().errorOrNull()

            assertTrue("$error", error is ApiError.Unauthorized)
            assertFalse("401 is never retried; it means re-pair (D-19)", error?.retryable ?: true)
        }
    }

    @Test
    fun aDroppedConnectionMapsToTransportAndStaysRetryable() = runBlocking {
        NetTestSupport.harness().use { h ->
            h.server.shutdown()

            val error = h.client.listAlbums().errorOrNull()

            assertTrue("$error", error is ApiError.Transport)
            assertTrue("network errors are retryable (D-19)", error?.retryable ?: false)
            assertNotNull((error as ApiError.Transport).cause)
        }
    }

    @Test
    fun callingBeforePairingMapsToNotConfiguredRatherThanTransport() = runBlocking {
        NetTestSupport.harness().use { h ->
            h.addressSource.clear()

            val error = h.client.listAlbums().errorOrNull()

            assertEquals(ApiError.NotConfigured, error)
            assertFalse("retrying without a paired server is pointless", ApiError.NotConfigured.retryable)
        }
    }

    // ------------------------------------------------------------------ helpers

    /** The HTTP status `02-API.md §2` pairs with each code. */
    private fun statusFor(code: String): Int = when (code) {
        ApiError.Codes.UNAUTHORIZED -> 401
        ApiError.Codes.PAIRING_CODE_INVALID -> 400
        ApiError.Codes.PAIRING_CODE_EXPIRED -> 410
        ApiError.Codes.PAIRING_RATE_LIMITED -> 429
        ApiError.Codes.BATCH_TOO_LARGE -> 400
        ApiError.Codes.RANGE_MISMATCH -> 409
        ApiError.Codes.HASH_MISMATCH -> 409
        ApiError.Codes.UNSUPPORTED_MEDIA_TYPE -> 415
        ApiError.Codes.FILE_TOO_LARGE -> 413
        ApiError.Codes.INSUFFICIENT_STORAGE -> 507
        ApiError.Codes.NOT_FOUND -> 404
        ApiError.Codes.RATE_LIMITED -> 429
        ApiError.Codes.INTERNAL -> 500
        else -> error("no status declared for $code")
    }

    private fun envelope(
        code: String,
        retryable: Boolean = false,
        details: Map<String, Any>? = null,
    ) = ErrorEnvelopeDto(
        ApiErrorDto(
            code = code,
            message = "Nachricht für $code",
            retryable = retryable,
            details = details?.let { map ->
                JsonObject(
                    map.mapValues { (_, value) ->
                        when (value) {
                            is Long -> JsonPrimitive(value)
                            else -> JsonPrimitive(value.toString())
                        }
                    },
                )
            },
        ),
    )
}
