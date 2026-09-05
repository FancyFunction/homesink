package de.homesink.app.data.net

import de.homesink.app.contract.SyncClient
import kotlinx.serialization.SerializationException
import kotlinx.serialization.json.Json
import okhttp3.RequestBody
import okhttp3.ResponseBody
import retrofit2.Response
import retrofit2.http.Body
import retrofit2.http.DELETE
import retrofit2.http.GET
import retrofit2.http.HEAD
import retrofit2.http.Header
import retrofit2.http.POST
import retrofit2.http.PUT
import retrofit2.http.Path
import retrofit2.http.Query
import retrofit2.http.Streaming
import java.io.IOException
import javax.inject.Inject
import javax.inject.Singleton

/**
 * The Retrofit surface, one method per `operationId` in `docs/api/openapi.yaml`
 * (WP-C5).
 *
 * Paths are relative to [ServerAddressInterceptor.PLACEHOLDER_BASE_URL], whose
 * authority is rewritten per request; `/healthz` deliberately sits outside the
 * `v1/` prefix, as the contract says.
 *
 * Every method returns `Response<T>` rather than `T` because the contract lives
 * in the status line and the headers as much as in the body — 204 plus
 * `X-Homesink-Received` is the whole of the resume probe. [HomesinkClient] wraps
 * this into [ApiResult]; call it rather than this interface unless you need the
 * raw streaming body.
 */
interface HomesinkApi {

    // ------------------------------------------------------------ pairing

    @POST("v1/pair")
    suspend fun pairDevice(@Body body: PairRequestDto): Response<PairResponseDto>

    @GET("v1/devices")
    suspend fun listDevices(): Response<List<DeviceDto>>

    @DELETE("v1/devices/{deviceId}")
    suspend fun revokeDevice(@Path("deviceId") deviceId: String): Response<Unit>

    // ------------------------------------------------------------ ingest

    @POST("v1/preflight")
    suspend fun preflight(@Body body: PreflightRequestDto): Response<PreflightResponseDto>

    @HEAD("v1/blobs/{hash}")
    suspend fun blobStatus(@Path("hash") hash: String): Response<Unit>

    @PUT("v1/blobs/{hash}")
    suspend fun uploadChunk(
        @Path("hash") hash: String,
        @Header("Content-Range") contentRange: String,
        @Body chunk: RequestBody,
    ): Response<Unit>

    @DELETE("v1/blobs/{hash}")
    suspend fun abortUpload(@Path("hash") hash: String): Response<Unit>

    @POST("v1/blobs/{hash}/commit")
    suspend fun commitBlob(
        @Path("hash") hash: String,
        @Body body: CommitRequestDto,
    ): Response<CommitResponseDto>

    // ------------------------------------------------------------ library

    @GET("v1/library/albums")
    suspend fun listAlbums(): Response<List<AlbumDto>>

    @GET("v1/library/albums/{album}/periods")
    suspend fun listPeriods(@Path("album") album: String): Response<List<PeriodDto>>

    @GET("v1/library/items")
    suspend fun listItems(
        @Query("album") album: String? = null,
        @Query("year") year: Int? = null,
        @Query("month") month: Int? = null,
        @Query("cursor") cursor: String? = null,
        @Query("limit") limit: Int? = null,
    ): Response<ItemPageDto>

    /** Caller owns the body and must close it. WP-C12 feeds this to Coil. */
    @Streaming
    @GET("v1/thumbs/{hash}")
    suspend fun getThumbnail(
        @Path("hash") hash: String,
        @Query("s") size: Int? = null,
    ): Response<ResponseBody>

    /** Caller owns the body and must close it. Range-capable (206). */
    @Streaming
    @GET("v1/media/{itemId}")
    suspend fun getMedia(
        @Path("itemId") itemId: String,
        @Query("variant") variant: String? = null,
        @Header("Range") range: String? = null,
    ): Response<ResponseBody>

    // ------------------------------------------------------------ app & system

    @GET("v1/app/latest")
    suspend fun getLatestApp(): Response<AppReleaseDto>

    /** Caller owns the body and must close it. WP-C14 verifies its SHA-256 before install (D-34). */
    @Streaming
    @GET("v1/app/download")
    suspend fun downloadApp(@Header("Range") range: String? = null): Response<ResponseBody>

    @GET("v1/system/status")
    suspend fun getSystemStatus(): Response<SystemStatusDto>

    @GET("v1/system/update")
    suspend fun getUpdateStatus(): Response<UpdateStatusDto>

    /** Outside the `/v1` prefix by design (02-API.md §6). */
    @GET("healthz")
    suspend fun healthz(): Response<ResponseBody>
}

/**
 * The client half of the HTTP contract (02-API.md §4), as [ApiResult]s.
 *
 * This is the type the rest of the app injects. It implements the frozen
 * `contract.SyncClient` marker so orchestrating packages (WP-C8) can depend on
 * the seam rather than on Retrofit (00-ARCHITECTURE.md §4.1).
 */
@Singleton
class HomesinkClient @Inject constructor(
    /** Exposed for the streaming endpoints, whose bodies their owning package must close. */
    val api: HomesinkApi,
    private val json: Json,
) : SyncClient {

    // ------------------------------------------------------------ pairing

    suspend fun pairDevice(request: PairRequestDto): ApiResult<PairResponseDto> =
        call { api.pairDevice(request) }

    suspend fun listDevices(): ApiResult<List<DeviceDto>> = call { api.listDevices() }

    suspend fun revokeDevice(deviceId: String): ApiResult<Unit> =
        callEmpty { api.revokeDevice(deviceId) }.discardBody()

    // ------------------------------------------------------------ ingest

    suspend fun preflight(request: PreflightRequestDto): ApiResult<PreflightResponseDto> =
        call { api.preflight(request) }

    /**
     * How many bytes the server already holds for [hash] (the resume probe).
     *
     * A [ApiError.NotFound] failure means "nothing staged" — the contract answers
     * 404 for an unknown hash — and the caller resumes from offset 0.
     */
    suspend fun blobStatus(hash: String): ApiResult<Long> =
        callEmpty { api.blobStatus(hash) }.mapReceivedBytes()

    /**
     * Appends one chunk. [start] and [endInclusive] are absolute offsets into the
     * file and [totalBytes] its full length, which is what `Content-Range` frames.
     * Succeeds with the server's confirmed byte count — invariant C2 says
     * `uploadedBytes` is written from this number, never from the local write count.
     */
    suspend fun uploadChunk(
        hash: String,
        start: Long,
        endInclusive: Long,
        totalBytes: Long,
        chunk: RequestBody,
    ): ApiResult<Long> =
        callEmpty {
            api.uploadChunk(hash, contentRange(start, endInclusive, totalBytes), chunk)
        }.mapReceivedBytes()

    suspend fun abortUpload(hash: String): ApiResult<Unit> =
        callEmpty { api.abortUpload(hash) }.discardBody()

    suspend fun commitBlob(hash: String, request: CommitRequestDto): ApiResult<CommitResponseDto> =
        call { api.commitBlob(hash, request) }

    // ------------------------------------------------------------ library

    suspend fun listAlbums(): ApiResult<List<AlbumDto>> = call { api.listAlbums() }

    suspend fun listPeriods(album: String): ApiResult<List<PeriodDto>> =
        call { api.listPeriods(album) }

    suspend fun listItems(
        album: String? = null,
        year: Int? = null,
        month: Int? = null,
        cursor: String? = null,
        limit: Int? = null,
    ): ApiResult<ItemPageDto> = call { api.listItems(album, year, month, cursor, limit) }

    // ------------------------------------------------------------ app & system

    suspend fun getLatestApp(): ApiResult<AppReleaseDto> = call { api.getLatestApp() }

    suspend fun getSystemStatus(): ApiResult<SystemStatusDto> = call { api.getSystemStatus() }

    suspend fun getUpdateStatus(): ApiResult<UpdateStatusDto> = call { api.getUpdateStatus() }

    // ------------------------------------------------------------ plumbing

    /** Runs a call whose success carries a JSON body. */
    private suspend fun <T : Any> call(block: suspend () -> Response<T>): ApiResult<T> =
        execute(block) { response ->
            val body = response.body()
            if (body == null) {
                ApiResult.Failure(ApiError.Unexpected(response.code(), EMPTY_BODY_CODE))
            } else {
                ApiResult.Success(body)
            }
        }

    /** Runs a call whose success is a 204 — the contract is in the headers. */
    private suspend fun callEmpty(block: suspend () -> Response<Unit>): ApiResult<Response<Unit>> =
        execute(block) { ApiResult.Success(it) }

    private suspend fun <T, R> execute(
        block: suspend () -> Response<T>,
        onSuccess: (Response<T>) -> ApiResult<R>,
    ): ApiResult<R> {
        val response = try {
            block()
        } catch (e: ServerNotConfiguredException) {
            return ApiResult.Failure(ApiError.NotConfigured)
        } catch (e: IOException) {
            return ApiResult.Failure(ApiError.Transport(e))
        }
        if (!response.isSuccessful) {
            return ApiResult.Failure(apiError(response))
        }
        return onSuccess(response)
    }

    /**
     * Error envelope → sealed [ApiError]; an unparsable body degrades to the
     * status alone. Public because the streaming endpoints on [api] are called
     * directly by their owning package (WP-C12, WP-C14), which must map their
     * failures the same way.
     */
    fun apiError(response: Response<*>): ApiError {
        val raw = try {
            response.errorBody()?.string()
        } catch (e: IOException) {
            null
        }
        if (raw.isNullOrBlank()) return ApiError.fromStatus(response.code())
        return try {
            ApiError.fromEnvelope(response.code(), json.decodeFromString(ErrorEnvelopeDto.serializer(), raw))
        } catch (e: SerializationException) {
            ApiError.fromStatus(response.code())
        }
    }

    /** Drops the empty 204 envelope; the call either happened or it failed. */
    private fun ApiResult<Response<Unit>>.discardBody(): ApiResult<Unit> = when (this) {
        is ApiResult.Failure -> this
        is ApiResult.Success -> ApiResult.Success(Unit)
    }

    /** Reads `X-Homesink-Received` off a 204; absent means the server confirmed nothing. */
    private fun ApiResult<Response<Unit>>.mapReceivedBytes(): ApiResult<Long> = when (this) {
        is ApiResult.Failure -> this
        is ApiResult.Success ->
            ApiResult.Success(value.headers()[HomesinkHeaders.RECEIVED]?.toLongOrNull() ?: 0L)
    }

    companion object {
        private const val EMPTY_BODY_CODE = "EMPTY_BODY"

        /** `bytes <start>-<endInclusive>/<total>` (02-API.md §4.2). */
        fun contentRange(start: Long, endInclusive: Long, totalBytes: Long): String =
            "bytes $start-$endInclusive/$totalBytes"
    }
}
