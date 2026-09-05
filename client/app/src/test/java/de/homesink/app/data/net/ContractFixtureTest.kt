package de.homesink.app.data.net

import kotlinx.coroutines.runBlocking
import kotlinx.serialization.KSerializer
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.mockwebserver.MockResponse
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * WP-C5 acceptance: "MockWebServer tests replay the **same** JSON fixtures the Go
 * tests use."
 *
 * `backend/internal/testutil/testdata/` is the single source of truth; the
 * `syncFixtures` Gradle task copies it byte-for-byte into this module's test
 * resources (08-ROADMAP.md §5). Every response fixture here is served by
 * MockWebServer and parsed by the shipping client, and every request fixture is
 * compared against what this client actually sends — so a field renamed on
 * either side breaks the other side's build.
 */
class ContractFixtureTest {

    // ------------------------------------------------------------------ cases

    /**
     * One replay: enqueue [fixture] with [status], make the call, and assert.
     * [operationId] ties the case back to the shared `manifest.json`.
     */
    private class Case(
        val operationId: String,
        val status: Int,
        val fixture: String,
        val play: suspend (NetTestSupport.Harness, ApiFixture) -> Unit,
    )

    /** The fixture body plus the parsed JSON, handed to each case. */
    private class ApiFixture(val body: String, val json: Json) {
        val element get() = json.parseToJsonElement(body)

        /** Round-trips [body] through [serializer] and fails if any field was lost. */
        fun <T> assertRoundTrips(serializer: KSerializer<T>, path: String): T {
            val decoded = json.decodeFromString(serializer, body)
            NetTestSupport.assertNoFieldLost(
                path,
                element,
                json.parseToJsonElement(json.encodeToString(serializer, decoded)),
            )
            return decoded
        }
    }

    private val successCases = listOf(
        Case("pairDevice", 201, "pairDevice.response-201.json") { h, f ->
            h.server.enqueue(jsonResponse(201, f.body))
            val result = h.client.pairDevice(
                PairRequestDto("483920", "Pixel 8", PairRequestDto.PLATFORM_ANDROID, 14),
            )
            val expected = f.assertRoundTrips(PairResponseDto.serializer(), "pairDevice.201")
            assertEquals(expected, result.getOrNull())
        },
        Case("listDevices", 200, "listDevices.response-200.json") { h, f ->
            h.server.enqueue(jsonResponse(200, f.body))
            val result = h.client.listDevices()
            val expected = f.assertRoundTrips(
                kotlinx.serialization.builtins.ListSerializer(DeviceDto.serializer()),
                "listDevices.200",
            )
            assertEquals(expected, result.getOrNull())
        },
        Case("preflight", 200, "preflight.response-200.json") { h, f ->
            h.server.enqueue(jsonResponse(200, f.body))
            val result = h.client.preflight(PreflightRequestDto(emptyList()))
            val expected = f.assertRoundTrips(PreflightResponseDto.serializer(), "preflight.200")
            assertEquals(expected, result.getOrNull())
            // The two verdicts the fixture carries are the two that need no bytes moved.
            val verdicts = expected.results.map { it.verdict }
            assertEquals(listOf(PreflightVerdictDto.RESUME, PreflightVerdictDto.DUPLICATE), verdicts)
        },
        Case("commitBlob", 201, "commitBlob.response-201.json") { h, f ->
            h.server.enqueue(jsonResponse(201, f.body))
            val result = h.client.commitBlob(HASH, commitRequest())
            val expected = f.assertRoundTrips(CommitResponseDto.serializer(), "commitBlob.201")
            assertEquals(expected, result.getOrNull())
        },
        Case("listAlbums", 200, "listAlbums.response-200.json") { h, f ->
            h.server.enqueue(jsonResponse(200, f.body))
            val result = h.client.listAlbums()
            val expected = f.assertRoundTrips(
                kotlinx.serialization.builtins.ListSerializer(AlbumDto.serializer()),
                "listAlbums.200",
            )
            assertEquals(expected, result.getOrNull())
        },
        Case("listPeriods", 200, "listPeriods.response-200.json") { h, f ->
            h.server.enqueue(jsonResponse(200, f.body))
            val result = h.client.listPeriods("Camera")
            val expected = f.assertRoundTrips(
                kotlinx.serialization.builtins.ListSerializer(PeriodDto.serializer()),
                "listPeriods.200",
            )
            assertEquals(expected, result.getOrNull())
        },
        Case("listItems", 200, "listItems.response-200.json") { h, f ->
            h.server.enqueue(jsonResponse(200, f.body))
            val result = h.client.listItems(album = "Camera", year = 2025, month = 8, limit = 100)
            val expected = f.assertRoundTrips(ItemPageDto.serializer(), "listItems.200")
            assertEquals(expected, result.getOrNull())
            assertNotNull("the first page carries a keyset cursor", expected.nextCursor)
        },
        Case("listItems", 200, "listItems.response-200-lastpage.json") { h, f ->
            h.server.enqueue(jsonResponse(200, f.body))
            val result = h.client.listItems(cursor = "MTc1NTQzNTMwMDAwMHxpdG1fMGY4ZTdkNmM=")
            val page = result.getOrNull()
            assertNotNull(page)
            assertEquals(emptyList<ItemDto>(), page?.items)
            assertNull("nextCursor is null on the last page", page?.nextCursor)
        },
        Case("getLatestApp", 200, "getLatestApp.response-200.json") { h, f ->
            h.server.enqueue(jsonResponse(200, f.body))
            val result = h.client.getLatestApp()
            val expected = f.assertRoundTrips(AppReleaseDto.serializer(), "getLatestApp.200")
            assertEquals(expected, result.getOrNull())
        },
        Case("getSystemStatus", 200, "getSystemStatus.response-200.json") { h, f ->
            h.server.enqueue(jsonResponse(200, f.body))
            val result = h.client.getSystemStatus()
            val expected = f.assertRoundTrips(SystemStatusDto.serializer(), "getSystemStatus.200")
            assertEquals(expected, result.getOrNull())
        },
        Case("getUpdateStatus", 200, "getUpdateStatus.response-200.json") { h, f ->
            h.server.enqueue(jsonResponse(200, f.body))
            val result = h.client.getUpdateStatus()
            val expected = f.assertRoundTrips(UpdateStatusDto.serializer(), "getUpdateStatus.200")
            assertEquals(expected, result.getOrNull())
            assertEquals(UpdateResultDto.UP_TO_DATE, expected.lastResult)
        },
    )

    private val errorCases = listOf(
        Case("pairDevice", 400, "pairDevice.response-400.json") { h, f ->
            h.server.enqueue(jsonResponse(400, f.body))
            val error = h.client.pairDevice(pairRequest()).errorOrNull()
            assertTrue("$error", error is ApiError.PairingCodeInvalid)
        },
        Case("pairDevice", 410, "pairDevice.response-410.json") { h, f ->
            h.server.enqueue(jsonResponse(410, f.body))
            val error = h.client.pairDevice(pairRequest()).errorOrNull()
            assertTrue("$error", error is ApiError.PairingCodeExpired)
        },
        Case("pairDevice", 429, "pairDevice.response-429.json") { h, f ->
            h.server.enqueue(jsonResponse(429, f.body))
            val error = h.client.pairDevice(pairRequest()).errorOrNull()
            assertTrue("$error", error is ApiError.PairingRateLimited)
            // D-01: the field is locked and counted down from this value.
            assertEquals(900_000L, (error as ApiError.PairingRateLimited).retryAfterMs)
        },
        Case("revokeDevice", 404, "revokeDevice.response-404.json") { h, f ->
            h.server.enqueue(jsonResponse(404, f.body))
            val error = h.client.revokeDevice("dev_3kf9p2r7").errorOrNull()
            assertTrue("$error", error is ApiError.NotFound)
        },
        Case("preflight", 400, "preflight.response-400.json") { h, f ->
            h.server.enqueue(jsonResponse(400, f.body))
            val error = h.client.preflight(PreflightRequestDto(emptyList())).errorOrNull()
            assertTrue("$error", error is ApiError.BatchTooLarge)
            assertEquals(PreflightRequestDto.MAX_ITEMS.toLong(), (error as ApiError.BatchTooLarge).limit)
        },
        Case("preflight", 507, "preflight.response-507.json") { h, f ->
            h.server.enqueue(jsonResponse(507, f.body))
            val error = h.client.preflight(PreflightRequestDto(emptyList())).errorOrNull()
            assertTrue("$error", error is ApiError.InsufficientStorage)
        },
        Case("blobStatus", 404, "blobStatus.response-404.json") { h, f ->
            // A HEAD response carries no body on the wire, so the resume probe
            // maps the bare status; the shared fixture's envelope is what a
            // body-carrying transport would deliver, and must map identically.
            h.server.enqueue(MockResponse().setResponseCode(404))
            val error = h.client.blobStatus(HASH).errorOrNull()
            assertTrue("$error", error is ApiError.NotFound)

            val envelope = f.json.decodeFromString(ErrorEnvelopeDto.serializer(), f.body)
            assertTrue(ApiError.fromEnvelope(404, envelope) is ApiError.NotFound)
        },
        Case("uploadChunk", 409, "uploadChunk.response-409.json") { h, f ->
            h.server.enqueue(jsonResponse(409, f.body))
            val error = h.client.uploadChunk(HASH, 0, 7, 8, chunk()).errorOrNull()
            assertTrue("$error", error is ApiError.RangeMismatch)
            // 02-API.md §4.2: the client self-heals in one round trip from this offset.
            assertEquals(16_777_216L, (error as ApiError.RangeMismatch).expectedStart)
        },
        Case("uploadChunk", 413, "uploadChunk.response-413.json") { h, f ->
            h.server.enqueue(jsonResponse(413, f.body))
            val error = h.client.uploadChunk(HASH, 0, 7, 8, chunk()).errorOrNull()
            assertTrue("$error", error is ApiError.FileTooLarge)
        },
        Case("uploadChunk", 507, "uploadChunk.response-507.json") { h, f ->
            h.server.enqueue(jsonResponse(507, f.body))
            val error = h.client.uploadChunk(HASH, 0, 7, 8, chunk()).errorOrNull()
            assertTrue("$error", error is ApiError.InsufficientStorage)
        },
        Case("commitBlob", 409, "commitBlob.response-409.json") { h, f ->
            h.server.enqueue(jsonResponse(409, f.body))
            val error = h.client.commitBlob(HASH, commitRequest()).errorOrNull()
            assertTrue("$error", error is ApiError.HashMismatch)
            // D-18: the client re-hashes against `expected` exactly once.
            assertEquals(HASH, (error as ApiError.HashMismatch).expected)
        },
        Case("getThumbnail", 404, "getThumbnail.response-404.json") { h, f ->
            h.server.enqueue(jsonResponse(404, f.body))
            val error = h.client.apiError(h.api.getThumbnail(HASH, THUMB_SIZE))
            assertTrue("$error", error is ApiError.NotFound)
        },
        Case("getMedia", 404, "getMedia.response-404.json") { h, f ->
            h.server.enqueue(jsonResponse(404, f.body))
            val error = h.client.apiError(h.api.getMedia("itm_9a8b7c6d"))
            assertTrue("$error", error is ApiError.NotFound)
        },
        Case("getLatestApp", 404, "getLatestApp.response-404.json") { h, f ->
            h.server.enqueue(jsonResponse(404, f.body))
            val error = h.client.getLatestApp().errorOrNull()
            assertTrue("$error", error is ApiError.NotFound)
        },
    )

    /** Fixtures whose contract is the status line and the headers, not a body. */
    private val headerCases = listOf(
        Case("blobStatus", 204, "blobStatus.response-204.json") { h, f ->
            h.server.enqueue(headerResponse(204, f))
            val result = h.client.blobStatus(HASH)
            assertEquals(16_777_216L, result.getOrNull())
        },
        Case("uploadChunk", 204, "uploadChunk.response-204.json") { h, f ->
            h.server.enqueue(headerResponse(204, f))
            val result = h.client.uploadChunk(HASH, 16_777_216, 25_165_823, 52_428_800, chunk())
            // Invariant C2: uploadedBytes comes from the server's count, never a local one.
            assertEquals(25_165_824L, result.getOrNull())
        },
        Case("abortUpload", 204, "abortUpload.response-204.json") { h, f ->
            h.server.enqueue(headerResponse(204, f))
            assertTrue(h.client.abortUpload(HASH) is ApiResult.Success)
        },
        Case("revokeDevice", 204, "revokeDevice.response-204.json") { h, f ->
            h.server.enqueue(headerResponse(204, f))
            assertTrue(h.client.revokeDevice("dev_7cq2m9x1") is ApiResult.Success)
        },
        Case("getThumbnail", 200, "getThumbnail.response-200.json") { h, f ->
            h.server.enqueue(headerResponse(200, f).setBody(WEBP_BYTES))
            val response = h.api.getThumbnail(HASH, THUMB_SIZE)
            assertEquals(200, response.code())
            // D-31 efficiency budget: content-addressed, immutable, one-year cache.
            assertEquals(
                f.headers()["Cache-Control"],
                response.headers()["Cache-Control"],
            )
            assertEquals(f.headers()["ETag"], response.headers()["ETag"])
            response.body()?.close()
        },
        Case("getThumbnail", 202, "getThumbnail.response-202.json") { h, f ->
            h.server.enqueue(headerResponse(202, f))
            val response = h.api.getThumbnail(HASH, THUMB_SIZE)
            assertEquals(202, response.code())
            assertEquals(f.headers()["Retry-After"], response.headers()["Retry-After"])
            response.body()?.close()
        },
        Case("getMedia", 200, "getMedia.response-200.json") { h, f ->
            h.server.enqueue(headerResponse(200, f))
            val response = h.api.getMedia("itm_9a8b7c6d")
            assertEquals(200, response.code())
            assertEquals("bytes", response.headers()["Accept-Ranges"])
            response.body()?.close()
        },
        Case("getMedia", 206, "getMedia.response-206.json") { h, f ->
            h.server.enqueue(headerResponse(206, f))
            val response = h.api.getMedia("itm_9a8b7c6d", range = "bytes=1000-2000")
            assertEquals(206, response.code())
            assertEquals(f.headers()["Content-Range"], response.headers()["Content-Range"])
            response.body()?.close()
            assertEquals("bytes=1000-2000", h.server.takeRequest().getHeader(HomesinkHeaders.RANGE))
        },
        Case("downloadApp", 200, "downloadApp.response-200.json") { h, f ->
            h.server.enqueue(headerResponse(200, f))
            val response = h.api.downloadApp()
            assertEquals(200, response.code())
            assertEquals(f.headers()["Content-Type"], response.headers()["Content-Type"])
            response.body()?.close()
        },
        Case("healthz", 200, "healthz.response-200.json") { h, f ->
            h.server.enqueue(headerResponse(200, f).setBody(f.bodyText()))
            val response = h.api.healthz()
            assertEquals(200, response.code())
            assertEquals("ok", response.body()?.string())
        },
        Case("healthz", 503, "healthz.response-503.json") { h, f ->
            h.server.enqueue(headerResponse(503, f).setBody(f.bodyText()))
            val response = h.api.healthz()
            assertEquals(503, response.code())
            // D-35: the rollback gate must be able to say why.
            assertEquals(f.bodyText(), response.errorBody()?.string())
        },
    )

    // ------------------------------------------------------------------ tests

    @Test
    fun mockWebServerReplaysEverySharedSuccessFixture() = replay(successCases)

    @Test
    fun mockWebServerReplaysEverySharedErrorFixture() = replay(errorCases)

    @Test
    fun mockWebServerReplaysEverySharedHeaderFixture() = replay(headerCases)

    @Test
    fun everyOperationInTheSharedManifestIsReplayedBySomeCase() {
        val manifest = NetworkModuleJson
            .parseToJsonElement(NetTestSupport.fixture("manifest.json"))
            .jsonObject

        val declared = manifest.operations().map { it.field("operationId") }.toSet()
        val replayed = (successCases + errorCases + headerCases).map { it.operationId }.toSet()

        assertEquals(
            "every operationId in the shared manifest must be replayed here " +
                "(08-ROADMAP.md §5); missing:",
            emptySet<String>(),
            declared - replayed,
        )
        assertEquals(
            "this suite replays an operation the shared manifest does not declare:",
            emptySet<String>(),
            replayed - declared,
        )
    }

    @Test
    fun everyResponseFixtureInTheSharedManifestIsReplayedBySomeCase() {
        val manifest = NetworkModuleJson
            .parseToJsonElement(NetTestSupport.fixture("manifest.json"))
            .jsonObject

        val declared = manifest.operations().flatMap { operation ->
            val responses = operation["responses"] as? kotlinx.serialization.json.JsonArray
                ?: error("manifest operation has no 'responses' array")
            responses.map { it.jsonObject.field("file") }
        }.toSet()
        val replayed = (successCases + errorCases + headerCases).map { it.fixture }.toSet()

        assertEquals(
            "every response fixture the Go suite validates must also be replayed here; missing:",
            emptySet<String>(),
            declared - replayed,
        )
    }

    @Test
    fun requestDtosSerialiseToTheSharedRequestFixtures() {
        val json = NetworkModuleJson

        // pairDevice.request.json
        val pair = json.decodeFromString(PairRequestDto.serializer(), NetTestSupport.fixture("pairDevice.request.json"))
        assertEquals(PairRequestDto("483920", "Pixel 8", "android", 14), pair)
        NetTestSupport.assertNoFieldLost(
            "pairDevice.request",
            json.parseToJsonElement(NetTestSupport.fixture("pairDevice.request.json")),
            json.parseToJsonElement(json.encodeToString(PairRequestDto.serializer(), pair)),
        )

        // preflight.request.json — the batch shape of D-16.
        val preflight = json.decodeFromString(
            PreflightRequestDto.serializer(),
            NetTestSupport.fixture("preflight.request.json"),
        )
        assertEquals(2, preflight.items.size)
        assertEquals(MediaTypeDto.VIDEO, preflight.items[0].mediaType)
        assertEquals(MediaTypeDto.IMAGE, preflight.items[1].mediaType)
        // D-11: offset travels with the timestamp so the server can derive YYYY/MM.
        assertEquals(120, preflight.items[0].capturedAtOffsetMin)
        NetTestSupport.assertNoFieldLost(
            "preflight.request",
            json.parseToJsonElement(NetTestSupport.fixture("preflight.request.json")),
            json.parseToJsonElement(json.encodeToString(PreflightRequestDto.serializer(), preflight)),
        )

        // commitBlob.request.json
        val commit = json.decodeFromString(
            CommitRequestDto.serializer(),
            NetTestSupport.fixture("commitBlob.request.json"),
        )
        NetTestSupport.assertNoFieldLost(
            "commitBlob.request",
            json.parseToJsonElement(NetTestSupport.fixture("commitBlob.request.json")),
            json.parseToJsonElement(json.encodeToString(CommitRequestDto.serializer(), commit)),
        )
    }

    @Test
    fun uploadChunkFramesContentRangeExactlyAsTheSharedRequestFixture() = runBlocking {
        val headers = NetworkModuleJson
            .parseToJsonElement(NetTestSupport.fixture("uploadChunk.request.json"))
            .jsonObject["headers"] as? JsonObject
            ?: error("uploadChunk.request.json has no 'headers' object")
        val expected = headers.field(HomesinkHeaders.CONTENT_RANGE)

        NetTestSupport.harness().use { h ->
            h.server.enqueue(MockResponse().setResponseCode(204))
            h.client.uploadChunk(HASH, 16_777_216, 25_165_823, 52_428_800, chunk())

            val sent = h.server.takeRequest()
            assertEquals("PUT", sent.method)
            assertEquals(expected, sent.getHeader(HomesinkHeaders.CONTENT_RANGE))
        }
    }

    // ------------------------------------------------------------------ helpers

    private fun replay(cases: List<Case>) = runBlocking {
        for (case in cases) {
            NetTestSupport.harness().use { harness ->
                val fixture = ApiFixture(NetTestSupport.fixture(case.fixture), harness.json)
                try {
                    case.play(harness, fixture)
                } catch (e: AssertionError) {
                    throw AssertionError("${case.operationId} ${case.status} (${case.fixture}): ${e.message}", e)
                }
            }
        }
    }

    /** The manifest's `operations` array, as objects. */
    private fun JsonObject.operations(): List<JsonObject> =
        (this["operations"] as? kotlinx.serialization.json.JsonArray
            ?: error("manifest.json has no 'operations' array")).map { it.jsonObject }

    private fun JsonObject.field(name: String): String =
        this[name]?.jsonPrimitive?.content ?: error("expected a '$name' field in $this")

    private fun ApiFixture.headers(): Map<String, String> =
        (element.jsonObject["headers"] as? JsonObject)
            ?.mapValues { it.value.jsonPrimitive.content }
            .orEmpty()

    private fun ApiFixture.bodyText(): String =
        element.jsonObject["body"]?.jsonPrimitive?.content.orEmpty()

    private fun headerResponse(status: Int, fixture: ApiFixture): MockResponse =
        MockResponse().setResponseCode(status).apply {
            fixture.headers().forEach { (name, value) -> addHeader(name, value) }
            // The fixture records the real body's length; MockWebServer supplies
            // the bytes, so Content-Length must not contradict it.
            removeHeader("Content-Length")
        }

    private fun jsonResponse(status: Int, body: String): MockResponse =
        MockResponse()
            .setResponseCode(status)
            .setHeader("Content-Type", "application/json")
            .setBody(body)

    private fun pairRequest() =
        PairRequestDto("483920", "Pixel 8", PairRequestDto.PLATFORM_ANDROID, 14)

    private fun commitRequest() = CommitRequestDto(
        clientId = "c_8831",
        filename = "VID_20250817_143201.mp4",
        album = "Camera",
        mimeType = "video/mp4",
        capturedAtEpochMs = 1_755_434_921_000,
        capturedAtOffsetMin = 120,
        mediaType = MediaTypeDto.VIDEO,
        width = 3840,
        height = 2160,
        durationMs = 42_000,
    )

    private fun chunk() = ByteArray(8).toRequestBody("application/octet-stream".toMediaType())

    private companion object {
        const val HASH = "5466fae56722d061d4d685bfee7b310f523ca684544cbf2cf03d9fffa53d810b"
        const val THUMB_SIZE = 256
        const val WEBP_BYTES = "RIFF"

        /** The exact `Json` the app ships, so the round-trip proves the shipping config. */
        val NetworkModuleJson: Json = de.homesink.app.di.NetworkModule.provideJson()
    }
}
