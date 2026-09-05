package de.homesink.app.data.net

import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.longOrNull

/**
 * The result of one API call: either a parsed body or a typed [ApiError] (WP-C5).
 *
 * Callers never see an HTTP status code or a raw exception — everything the sync
 * engine needs to decide "retry, skip, or re-pair" (D-19) is on [ApiError].
 */
sealed interface ApiResult<out T> {

    data class Success<out T>(val value: T) : ApiResult<T>

    data class Failure(val error: ApiError) : ApiResult<Nothing>
}

/** The value on success, `null` on failure. */
fun <T> ApiResult<T>.getOrNull(): T? = when (this) {
    is ApiResult.Success -> value
    is ApiResult.Failure -> null
}

/** The error on failure, `null` on success. */
fun <T> ApiResult<T>.errorOrNull(): ApiError? = when (this) {
    is ApiResult.Success -> null
    is ApiResult.Failure -> error
}

/**
 * Every failure this client can produce, one subtype per error code in
 * `02-API.md §2` — the Kotlin equivalent of `backend/internal/core/errors.go`.
 *
 * [serverMessage] is the server's already-German text (D-37); it is the string a
 * screen may show verbatim. Failures that never reached a server carry an empty
 * [serverMessage] and the UI selects a string resource by [code] instead.
 *
 * [retryable] comes off the wire so the server, not the client, decides the
 * retry class; the client-side subtypes state their own.
 */
sealed class ApiError {

    /** Wire code (or a synthetic `CLIENT_*` code). Diagnostic, never user-visible. */
    abstract val code: String

    /** The server's localised message; empty when no server responded. */
    abstract val serverMessage: String

    /** Whether D-19 permits another attempt. */
    abstract val retryable: Boolean

    /** 401 — drop the token and re-pair (`sync_problem` notification). */
    data class Unauthorized(
        override val serverMessage: String = "",
        override val retryable: Boolean = false,
    ) : ApiError() {
        override val code: String get() = Codes.UNAUTHORIZED
    }

    /** 400 — the entered pairing code is wrong; ask for another. */
    data class PairingCodeInvalid(
        override val serverMessage: String = "",
        override val retryable: Boolean = false,
    ) : ApiError() {
        override val code: String get() = Codes.PAIRING_CODE_INVALID
    }

    /** 410 — the pairing code's 10-minute TTL ran out (D-01). */
    data class PairingCodeExpired(
        override val serverMessage: String = "",
        override val retryable: Boolean = false,
    ) : ApiError() {
        override val code: String get() = Codes.PAIRING_CODE_EXPIRED
    }

    /** 429 — five wrong attempts; lock the field and count [retryAfterMs] down (D-01). */
    data class PairingRateLimited(
        val retryAfterMs: Long?,
        override val serverMessage: String = "",
        override val retryable: Boolean = true,
    ) : ApiError() {
        override val code: String get() = Codes.PAIRING_RATE_LIMITED
    }

    /** 400 — more than [PreflightRequestDto.MAX_ITEMS] items in one preflight (02-API.md §4.1). */
    data class BatchTooLarge(
        val limit: Long?,
        val received: Long?,
        override val serverMessage: String = "",
        override val retryable: Boolean = false,
    ) : ApiError() {
        override val code: String get() = Codes.BATCH_TOO_LARGE
    }

    /** 409 — the chunk did not start at [expectedStart]; resume from there (02-API.md §4.2). */
    data class RangeMismatch(
        val expectedStart: Long?,
        val receivedStart: Long?,
        override val serverMessage: String = "",
        override val retryable: Boolean = true,
    ) : ApiError() {
        override val code: String get() = Codes.RANGE_MISMATCH
    }

    /** 409 — the staged file was destroyed; re-hash locally exactly once, then fail the file (D-18). */
    data class HashMismatch(
        val expected: String?,
        val actual: String?,
        override val serverMessage: String = "",
        override val retryable: Boolean = true,
    ) : ApiError() {
        override val code: String get() = Codes.HASH_MISMATCH
    }

    /** 415 — outside the D-14 allowlist; mark the file skipped and never re-offer it. */
    data class UnsupportedMediaType(
        val mimeType: String?,
        override val serverMessage: String = "",
        override val retryable: Boolean = false,
    ) : ApiError() {
        override val code: String get() = Codes.UNSUPPORTED_MEDIA_TYPE
    }

    /** 413 — above `HOMESINK_MAX_FILE_BYTES` (D-21); skip and explain in the queue screen. */
    data class FileTooLarge(
        val maxBytes: Long?,
        override val serverMessage: String = "",
        override val retryable: Boolean = false,
    ) : ApiError() {
        override val code: String get() = Codes.FILE_TOO_LARGE
    }

    /** 507 — the disk guard tripped (D-20); abort the whole run. */
    data class InsufficientStorage(
        val freeBytes: Long?,
        val requiredBytes: Long?,
        override val serverMessage: String = "",
        override val retryable: Boolean = false,
    ) : ApiError() {
        override val code: String get() = Codes.INSUFFICIENT_STORAGE
    }

    /** 404 — refresh the list. */
    data class NotFound(
        override val serverMessage: String = "",
        override val retryable: Boolean = false,
    ) : ApiError() {
        override val code: String get() = Codes.NOT_FOUND
    }

    /** 429 on a non-pairing endpoint. */
    data class RateLimited(
        val retryAfterMs: Long?,
        override val serverMessage: String = "",
        override val retryable: Boolean = true,
    ) : ApiError() {
        override val code: String get() = Codes.RATE_LIMITED
    }

    /** 500 — back off per D-19. */
    data class Internal(
        override val serverMessage: String = "",
        override val retryable: Boolean = true,
    ) : ApiError() {
        override val code: String get() = Codes.INTERNAL
    }

    /**
     * No response at all: socket closed, DNS failure, TLS pin mismatch, timeout.
     * Retryable per D-19 ("network errors").
     */
    data class Transport(val cause: Throwable) : ApiError() {
        override val code: String get() = Codes.CLIENT_TRANSPORT
        override val serverMessage: String get() = ""
        override val retryable: Boolean get() = true
    }

    /** No paired server address is configured yet — nothing to talk to (D-03). */
    data object NotConfigured : ApiError() {
        override val code: String get() = Codes.CLIENT_NOT_CONFIGURED
        override val serverMessage: String get() = ""
        override val retryable: Boolean get() = false
    }

    /**
     * A status or code the contract does not describe — a newer server, or a
     * proxy in the way. Treated as retryable only for 5xx (02-API.md §7.2:
     * an old client must degrade, not crash).
     */
    data class Unexpected(
        val httpStatus: Int,
        override val code: String,
        override val serverMessage: String = "",
        override val retryable: Boolean = httpStatus >= 500,
    ) : ApiError()

    /** The wire codes of `02-API.md §2` plus the two client-side synthetics. */
    object Codes {
        const val UNAUTHORIZED = "UNAUTHORIZED"
        const val PAIRING_CODE_INVALID = "PAIRING_CODE_INVALID"
        const val PAIRING_CODE_EXPIRED = "PAIRING_CODE_EXPIRED"
        const val PAIRING_RATE_LIMITED = "PAIRING_RATE_LIMITED"
        const val BATCH_TOO_LARGE = "BATCH_TOO_LARGE"
        const val RANGE_MISMATCH = "RANGE_MISMATCH"
        const val HASH_MISMATCH = "HASH_MISMATCH"
        const val UNSUPPORTED_MEDIA_TYPE = "UNSUPPORTED_MEDIA_TYPE"
        const val FILE_TOO_LARGE = "FILE_TOO_LARGE"
        const val INSUFFICIENT_STORAGE = "INSUFFICIENT_STORAGE"
        const val NOT_FOUND = "NOT_FOUND"
        const val RATE_LIMITED = "RATE_LIMITED"
        const val INTERNAL = "INTERNAL"

        const val CLIENT_TRANSPORT = "CLIENT_TRANSPORT"
        const val CLIENT_NOT_CONFIGURED = "CLIENT_NOT_CONFIGURED"

        /** Every code the server may send, in `02-API.md §2` / openapi `ErrorEnvelope` order. */
        val WIRE: List<String> = listOf(
            UNAUTHORIZED,
            PAIRING_CODE_INVALID,
            PAIRING_CODE_EXPIRED,
            PAIRING_RATE_LIMITED,
            BATCH_TOO_LARGE,
            RANGE_MISMATCH,
            HASH_MISMATCH,
            UNSUPPORTED_MEDIA_TYPE,
            FILE_TOO_LARGE,
            INSUFFICIENT_STORAGE,
            NOT_FOUND,
            RATE_LIMITED,
            INTERNAL,
        )
    }

    companion object {

        /**
         * Maps one parsed error envelope onto its subtype. An unknown code
         * becomes [Unexpected] carrying the code verbatim, so a newer server's
         * vocabulary is logged rather than swallowed (02-API.md §7.2).
         */
        fun fromEnvelope(httpStatus: Int, envelope: ErrorEnvelopeDto): ApiError {
            val body = envelope.error
            val details = body.details
            val message = body.message
            val retryable = body.retryable
            return when (body.code) {
                Codes.UNAUTHORIZED -> Unauthorized(message, retryable)
                Codes.PAIRING_CODE_INVALID -> PairingCodeInvalid(message, retryable)
                Codes.PAIRING_CODE_EXPIRED -> PairingCodeExpired(message, retryable)
                Codes.PAIRING_RATE_LIMITED -> PairingRateLimited(
                    retryAfterMs = body.retryAfterMs ?: details.long("retryAfterMs"),
                    serverMessage = message,
                    retryable = retryable,
                )
                Codes.BATCH_TOO_LARGE -> BatchTooLarge(
                    limit = details.long("limit"),
                    received = details.long("received"),
                    serverMessage = message,
                    retryable = retryable,
                )
                Codes.RANGE_MISMATCH -> RangeMismatch(
                    expectedStart = details.long("expectedStart"),
                    receivedStart = details.long("receivedStart"),
                    serverMessage = message,
                    retryable = retryable,
                )
                Codes.HASH_MISMATCH -> HashMismatch(
                    expected = details.string("expected"),
                    actual = details.string("actual"),
                    serverMessage = message,
                    retryable = retryable,
                )
                Codes.UNSUPPORTED_MEDIA_TYPE -> UnsupportedMediaType(
                    mimeType = details.string("mimeType"),
                    serverMessage = message,
                    retryable = retryable,
                )
                Codes.FILE_TOO_LARGE -> FileTooLarge(
                    maxBytes = details.long("maxBytes"),
                    serverMessage = message,
                    retryable = retryable,
                )
                Codes.INSUFFICIENT_STORAGE -> InsufficientStorage(
                    freeBytes = details.long("freeBytes"),
                    requiredBytes = details.long("requiredBytes"),
                    serverMessage = message,
                    retryable = retryable,
                )
                Codes.NOT_FOUND -> NotFound(message, retryable)
                Codes.RATE_LIMITED -> RateLimited(
                    retryAfterMs = body.retryAfterMs ?: details.long("retryAfterMs"),
                    serverMessage = message,
                    retryable = retryable,
                )
                Codes.INTERNAL -> Internal(message, retryable)
                else -> Unexpected(httpStatus, body.code, message, retryable)
            }
        }

        /**
         * Fallback when a non-2xx response carries no parsable envelope — a
         * gateway page, an empty body, or a truncated response.
         */
        fun fromStatus(httpStatus: Int): ApiError = when (httpStatus) {
            HTTP_UNAUTHORIZED -> Unauthorized()
            HTTP_NOT_FOUND -> NotFound()
            else -> Unexpected(httpStatus, "HTTP_$httpStatus")
        }

        /** 401. The one status the auth interceptor reacts to (D-01). */
        const val HTTP_UNAUTHORIZED: Int = 401

        private const val HTTP_NOT_FOUND: Int = 404

        private fun JsonObject?.long(key: String): Long? =
            this?.get(key)?.jsonPrimitive?.longOrNull

        private fun JsonObject?.string(key: String): String? =
            this?.get(key)?.jsonPrimitive?.contentOrNullSafe()

        private fun kotlinx.serialization.json.JsonPrimitive.contentOrNullSafe(): String? =
            if (isString) content else null
    }
}
