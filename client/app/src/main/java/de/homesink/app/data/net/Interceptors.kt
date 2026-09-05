package de.homesink.app.data.net

import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import okhttp3.Interceptor
import okhttp3.Response
import java.io.IOException
import java.util.concurrent.atomic.AtomicReference
import javax.inject.Inject
import javax.inject.Singleton

/**
 * OkHttp interceptors and the small mutable seams they read (WP-C5).
 *
 * Three interceptors, in the order `NetworkModule` installs them:
 *  1. [ServerAddressInterceptor] — rewrites the placeholder host onto the paired
 *     server, so a DHCP change or an mDNS re-resolution costs no rebuild (D-03).
 *  2. [ClientVersionInterceptor] — `X-Homesink-Client` on every request (D-33).
 *  3. [AuthInterceptor] — `Authorization: Bearer`, and the one-shot token drop on 401 (D-01).
 *  4. [LatestVersionInterceptor] — parses `X-Homesink-Latest` off any response (D-33).
 */

/** The `X-Homesink-*` header names of 02-API.md §1. */
object HomesinkHeaders {
    const val CLIENT = "X-Homesink-Client"
    const val LATEST = "X-Homesink-Latest"
    const val TIME = "X-Homesink-Time"
    const val RECEIVED = "X-Homesink-Received"
    const val REQUEST_ID = "X-Request-Id"
    const val AUTHORIZATION = "Authorization"
    const val CONTENT_RANGE = "Content-Range"
    const val RANGE = "Range"
}

// ------------------------------------------------------------------ server address

/**
 * Where the paired server currently lives.
 *
 * @param useTls false only for the `HOMESINK_TLS=off` debugging downgrade (D-02).
 * @param spkiSha256 base64 SHA-256 of the server cert's SPKI, from the pairing
 *   response — the pin. `null` when TLS is off.
 */
data class ServerAddress(
    val host: String,
    val port: Int,
    val useTls: Boolean = true,
    val spkiSha256: String? = null,
)

/**
 * Reads the current [ServerAddress]. WP-C6 owns where it is persisted
 * (`server_host`/`server_port`/`server_spki` in DataStore, 03-DATA-MODEL.md §2.3)
 * and pushes it into [MutableServerAddressSource]; this package only reads it.
 */
fun interface ServerAddressSource {
    fun address(): ServerAddress?
}

/**
 * The process-wide holder WP-C6 writes after pairing, after a successful mDNS
 * re-resolution, and once at startup when it rehydrates from DataStore.
 */
@Singleton
class MutableServerAddressSource @Inject constructor() : ServerAddressSource {

    private val current = AtomicReference<ServerAddress?>(null)

    override fun address(): ServerAddress? = current.get()

    fun set(address: ServerAddress) {
        current.set(address)
    }

    fun clear() {
        current.set(null)
    }
}

/** Thrown when a call is made before any server is paired; surfaces as [ApiError.NotConfigured]. */
class ServerNotConfiguredException : IOException("no paired server address")

/**
 * Rewrites [PLACEHOLDER_BASE_URL]'s scheme/host/port onto the current
 * [ServerAddress]. Retrofit fixes its base URL at construction, but the sink's
 * address is discovered at pairing time and can change with the DHCP lease
 * (D-03) — rewriting per request is what keeps one singleton Retrofit correct.
 */
@Singleton
class ServerAddressInterceptor @Inject constructor(
    private val source: ServerAddressSource,
) : Interceptor {

    override fun intercept(chain: Interceptor.Chain): Response {
        val address = source.address() ?: throw ServerNotConfiguredException()
        val rewritten = chain.request().newBuilder()
            .url(
                chain.request().url.newBuilder()
                    .scheme(if (address.useTls) "https" else "http")
                    .host(address.host)
                    .port(address.port)
                    .build(),
            )
            .build()
        return chain.proceed(rewritten)
    }

    companion object {
        /**
         * Never resolved: `.invalid` is reserved by RFC 6761 precisely so a
         * placeholder cannot accidentally reach a real host. Every request's
         * authority is replaced before it leaves the interceptor chain.
         */
        const val PLACEHOLDER_BASE_URL: String = "https://homesink.invalid/"
    }
}

// ------------------------------------------------------------------ client version

/** This app's `versionCode`/`versionName`, as sent in `X-Homesink-Client` (D-33). */
data class ClientVersion(val versionCode: Int, val versionName: String) {
    /** The header value: `<code>/<name>` (02-API.md §1). */
    val headerValue: String get() = "$versionCode/$versionName"
}

/**
 * Stamps `X-Homesink-Client` on every request. The server answers with
 * `X-Homesink-Latest` when something newer exists, which is why the update check
 * costs zero extra round trips (D-33).
 */
@Singleton
class ClientVersionInterceptor @Inject constructor(
    private val clientVersion: ClientVersion,
) : Interceptor {

    override fun intercept(chain: Interceptor.Chain): Response =
        chain.proceed(
            chain.request().newBuilder()
                .header(HomesinkHeaders.CLIENT, clientVersion.headerValue)
                .build(),
        )
}

// ------------------------------------------------------------------ auth

/**
 * The device token seam (D-01). WP-C6 owns persistence — the token is the one
 * secret and lives in `EncryptedSharedPreferences`, not DataStore
 * (03-DATA-MODEL.md §2.3) — and hydrates [InMemoryTokenStore] from it.
 *
 * Implementations must never log the token (00-ARCHITECTURE.md §6).
 */
interface TokenStore {
    /** The current device token, or `null` when this device is not paired. */
    fun token(): String?

    /** Forgets the token. Called by [AuthInterceptor] on a 401, and by WP-C6 on unpair. */
    fun clear()
}

/**
 * In-memory holder for the device token. WP-C6 calls [set] after pairing and
 * after reading the persisted token at startup; this package only reads it and
 * clears it when the server rejects it.
 */
@Singleton
class InMemoryTokenStore @Inject constructor() : TokenStore {

    private val current = AtomicReference<String?>(null)

    override fun token(): String? = current.get()

    override fun clear() {
        current.set(null)
    }

    fun set(token: String) {
        current.set(token)
    }
}

/**
 * Adds `Authorization: Bearer hs_…` when a token exists, and drops the token on
 * a 401 — **exactly once per token**.
 *
 * The once-per-token latch matters because a sync run has three uploads in
 * flight (D-17): a revoked device gets three simultaneous 401s, and clearing
 * three times would race WP-C6's re-pairing, wiping a freshly stored token. The
 * latch remembers which token was rejected, so a new token re-arms it.
 */
@Singleton
class AuthInterceptor @Inject constructor(
    private val tokenStore: TokenStore,
) : Interceptor {

    private val clearedToken = AtomicReference<String?>(null)

    override fun intercept(chain: Interceptor.Chain): Response {
        val token = tokenStore.token()
        val request = if (token == null) {
            chain.request()
        } else {
            chain.request().newBuilder()
                .header(HomesinkHeaders.AUTHORIZATION, "Bearer $token")
                .build()
        }

        val response = chain.proceed(request)
        if (response.code == ApiError.HTTP_UNAUTHORIZED && token != null &&
            clearedToken.getAndSet(token) != token
        ) {
            tokenStore.clear()
        }
        return response
    }
}

// ------------------------------------------------------------------ update availability

/** A newer APK the server advertised in `X-Homesink-Latest` (D-33/D-34). */
data class UpdateInfo(
    val versionCode: Int,
    val versionName: String,
    /** Lowercase-hex SHA-256; WP-C14 verifies the download against it before install (D-34). */
    val sha256: String,
    val downloadUrl: String,
)

/**
 * The shared flow WP-C14 renders its banner from. Nothing polls: the value is
 * published by [LatestVersionInterceptor] from whatever response happened to
 * come back (D-33).
 */
@Singleton
class UpdateAvailability @Inject constructor() {

    private val state = MutableStateFlow<UpdateInfo?>(null)

    /** The newest release the server has advertised so far; `null` until it advertises one. */
    val latest: StateFlow<UpdateInfo?> = state.asStateFlow()

    /** Publishes [info], keeping whichever `versionCode` is higher (D-34: code decides, name is cosmetic). */
    fun publish(info: UpdateInfo) {
        while (true) {
            val seen = state.value
            if (seen != null && seen.versionCode >= info.versionCode) return
            if (state.compareAndSet(seen, info)) return
        }
    }
}

/**
 * Parses `X-Homesink-Latest: <code>;<name>;<sha256>;<url>` off **any** response
 * and publishes it to [UpdateAvailability] (D-33). A malformed value is ignored
 * rather than thrown: an update hint must never fail the call it rode along on.
 */
@Singleton
class LatestVersionInterceptor @Inject constructor(
    private val updateAvailability: UpdateAvailability,
) : Interceptor {

    override fun intercept(chain: Interceptor.Chain): Response {
        val response = chain.proceed(chain.request())
        response.header(HomesinkHeaders.LATEST)?.let { header ->
            parse(header)?.let(updateAvailability::publish)
        }
        return response
    }

    companion object {
        private const val FIELD_COUNT = 4

        /** `<versionCode>;<versionName>;<sha256>;<downloadUrl>`, or `null` if it does not parse. */
        fun parse(header: String): UpdateInfo? {
            val parts = header.split(';')
            if (parts.size != FIELD_COUNT) return null
            val versionCode = parts[0].trim().toIntOrNull() ?: return null
            val versionName = parts[1].trim()
            val sha256 = parts[2].trim()
            val downloadUrl = parts[3].trim()
            if (versionName.isEmpty() || sha256.isEmpty() || downloadUrl.isEmpty()) return null
            return UpdateInfo(versionCode, versionName, sha256, downloadUrl)
        }
    }
}
