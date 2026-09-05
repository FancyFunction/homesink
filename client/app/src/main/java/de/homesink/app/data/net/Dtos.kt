package de.homesink.app.data.net

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.JsonObject

/**
 * Wire DTOs, field-for-field from `docs/api/openapi.yaml` (WP-C5).
 *
 * Rules that make these safe to evolve (02-API.md §7):
 *  - `camelCase`, exactly the names in the schema — a rename on either side must
 *    break the shared-fixture test, which is the only cheap way to keep Go and
 *    Kotlin honest about one contract (08-ROADMAP.md §5).
 *  - Timestamps are epoch **milliseconds** (`Long`), never strings, never seconds.
 *  - Optional schema properties are nullable with a `null` default so an old app
 *    parses a new server's payload; unknown fields are dropped by the [Json]
 *    instance `NetworkModule` configures, not by these types.
 *
 * These are transport types. They never leak into the UI — `contract/` holds the
 * domain vocabulary (00-ARCHITECTURE.md §4.1).
 */

// ---------------------------------------------------------------- errors

/** The single error shape crossing the wire (02-API.md §2). */
@Serializable
data class ErrorEnvelopeDto(
    val error: ApiErrorDto,
)

/** Body of [ErrorEnvelopeDto]. `details` stays an opaque object; each [ApiError] reads the keys it owns. */
@Serializable
data class ApiErrorDto(
    val code: String,
    val message: String,
    val retryable: Boolean,
    val retryAfterMs: Long? = null,
    val details: JsonObject? = null,
)

// ---------------------------------------------------------------- pairing

@Serializable
data class PairRequestDto(
    val code: String,
    val deviceName: String,
    val platform: String,
    val appVersionCode: Int,
) {
    companion object {
        /** The only `platform` this client ever sends (openapi `PairRequest.platform` enum). */
        const val PLATFORM_ANDROID: String = "android"
    }
}

@Serializable
data class PairResponseDto(
    val token: String,
    val deviceId: String,
    val serverId: String,
    val serverName: String,
    /** base64 SHA-256 of the server cert's SPKI — the pin (D-02). */
    val tlsSpkiSha256: String,
    val serverTimeMs: Long,
)

@Serializable
data class DeviceDto(
    val deviceId: String,
    val name: String,
    val lastSeenMs: Long,
    val appVersionCode: Int? = null,
    /** True for the calling device. */
    val current: Boolean,
)

// ---------------------------------------------------------------- ingest

/** openapi `MediaType`; the lowercase wire spelling of `contract.MediaType`. */
@Serializable
enum class MediaTypeDto {
    @SerialName("image")
    IMAGE,

    @SerialName("video")
    VIDEO,

    @SerialName("audio")
    AUDIO,
}

@Serializable
data class PreflightItemDto(
    /** Opaque client correlation id, echoed back in [PreflightResultDto.clientId]. */
    val clientId: String,
    val hash: String,
    val sizeBytes: Long,
    val filename: String,
    val mimeType: String,
    /** Raw bucket name; the server sanitises it (D-09/D-10). */
    val album: String,
    val capturedAtEpochMs: Long,
    /** Minutes east of UTC at capture time (D-11). */
    val capturedAtOffsetMin: Int,
    val mediaType: MediaTypeDto,
    val width: Int? = null,
    val height: Int? = null,
    val durationMs: Long? = null,
)

@Serializable
data class PreflightRequestDto(
    val items: List<PreflightItemDto>,
) {
    companion object {
        /** Max items per call; beyond this the server answers 400 `BATCH_TOO_LARGE` (D-16, 02-API.md §4.1). */
        const val MAX_ITEMS: Int = 500
    }
}

/** openapi `PreflightResult.verdict`. */
@Serializable
enum class PreflightVerdictDto {
    @SerialName("upload")
    UPLOAD,

    @SerialName("duplicate")
    DUPLICATE,

    @SerialName("resume")
    RESUME,

    @SerialName("reject")
    REJECT,
}

@Serializable
data class PreflightResultDto(
    val clientId: String,
    val verdict: PreflightVerdictDto,
    /** Present for `resume`. */
    val receivedBytes: Long? = null,
    /** Present for `duplicate` — the item the server created for this device (D-07). */
    val itemId: String? = null,
    /** Present for `duplicate`. */
    val libraryPath: String? = null,
    /** Present for `reject`; one of the 02-API.md §2 codes. */
    val code: String? = null,
)

@Serializable
data class PreflightResponseDto(
    val results: List<PreflightResultDto>,
    val serverTimeMs: Long,
)

@Serializable
data class CommitRequestDto(
    val clientId: String,
    val filename: String,
    val album: String,
    val mimeType: String,
    val capturedAtEpochMs: Long,
    val capturedAtOffsetMin: Int,
    val mediaType: MediaTypeDto,
    val width: Int? = null,
    val height: Int? = null,
    val durationMs: Long? = null,
)

@Serializable
data class CommitResponseDto(
    val itemId: String,
    /** Path relative to the library root, e.g. `Camera/2025/08/VID_…mp4`. */
    val libraryPath: String,
    val deduplicated: Boolean,
    val transcodeQueued: Boolean,
)

// ---------------------------------------------------------------- library

@Serializable
data class AlbumDto(
    val album: String,
    val itemCount: Int,
    val sizeBytes: Long,
    val latestCapturedAtMs: Long? = null,
    val coverHash: String? = null,
)

@Serializable
data class PeriodDto(
    val year: Int,
    val month: Int,
    val itemCount: Int,
    val sizeBytes: Long,
)

@Serializable
data class ItemDto(
    val itemId: String,
    val hash: String,
    val filename: String,
    val album: String,
    val year: Int,
    val month: Int,
    val mediaType: MediaTypeDto,
    val mimeType: String,
    val sizeBytes: Long,
    val width: Int? = null,
    val height: Int? = null,
    val durationMs: Long? = null,
    val capturedAtMs: Long,
    val hasTranscode: Boolean,
)

@Serializable
data class ItemPageDto(
    val items: List<ItemDto>,
    /** Opaque keyset cursor; `null` on the last page (02-API.md §5). */
    val nextCursor: String? = null,
)

// ---------------------------------------------------------------- app & system

@Serializable
data class AppReleaseDto(
    val versionCode: Int,
    val versionName: String,
    /** The client MUST verify the downloaded APK against this before install (D-34). */
    val sha256: String,
    val sizeBytes: Long,
    val minSdk: Int? = null,
    val releaseNotes: String? = null,
    val downloadUrl: String,
)

@Serializable
data class SystemStatusDto(
    val serverVersion: String? = null,
    val uptimeSec: Long? = null,
    val itemCount: Int? = null,
    val blobCount: Int? = null,
    val libraryBytes: Long? = null,
    val freeBytes: Long? = null,
    val jobsQueued: Int? = null,
    val jobsRunning: Int? = null,
    val jobsFailed: Int? = null,
)

/** openapi `UpdateStatus.lastResult`. */
@Serializable
enum class UpdateResultDto {
    @SerialName("up_to_date")
    UP_TO_DATE,

    @SerialName("updated")
    UPDATED,

    @SerialName("failed")
    FAILED,

    @SerialName("rolled_back")
    ROLLED_BACK,

    @SerialName("disabled")
    DISABLED,
}

@Serializable
data class UpdateStatusDto(
    val current: String? = null,
    val latestAvailable: String? = null,
    val lastCheckMs: Long? = null,
    val lastResult: UpdateResultDto? = null,
)
