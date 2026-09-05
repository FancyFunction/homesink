package de.homesink.app.di

import android.content.Context
import androidx.core.content.pm.PackageInfoCompat
import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.android.qualifiers.ApplicationContext
import dagger.hilt.components.SingletonComponent
import de.homesink.app.data.net.AuthInterceptor
import de.homesink.app.data.net.ClientVersion
import de.homesink.app.data.net.ClientVersionInterceptor
import de.homesink.app.data.net.HomesinkApi
import de.homesink.app.data.net.InMemoryTokenStore
import de.homesink.app.data.net.LatestVersionInterceptor
import de.homesink.app.data.net.MutableServerAddressSource
import de.homesink.app.data.net.PinnedCallFactory
import de.homesink.app.data.net.ServerAddressInterceptor
import de.homesink.app.data.net.ServerAddressSource
import de.homesink.app.data.net.TokenStore
import kotlinx.serialization.json.Json
import okhttp3.Call
import okhttp3.ConnectionPool
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Protocol
import retrofit2.Retrofit
import com.jakewharton.retrofit2.converter.kotlinx.serialization.asConverterFactory
import java.util.concurrent.TimeUnit
import javax.inject.Singleton

/**
 * One Hilt module for the `data/net` feature package (00-ARCHITECTURE.md §4.1,
 * 08-ROADMAP.md §4: one module per feature package, so DI never collides).
 *
 * Two seams are deliberately mutable holders rather than DataStore reads:
 * [MutableServerAddressSource] and [InMemoryTokenStore]. WP-C6 owns persistence
 * (DataStore for the address, `EncryptedSharedPreferences` for the token, per
 * 03-DATA-MODEL.md §2.3) and pushes both in — after pairing, after an mDNS
 * re-resolution, and once at startup. Keeping the read side synchronous is what
 * lets an OkHttp interceptor consult them without blocking on a Flow.
 */
@Module
@InstallIn(SingletonComponent::class)
object NetworkModule {

    /** Seconds a non-transfer call may take end to end (WP-C5). */
    private const val CALL_TIMEOUT_SECONDS = 30L

    private const val CONNECT_TIMEOUT_SECONDS = 10L

    /**
     * The pool has to outlive a whole sync run: three files in flight (D-17)
     * across hundreds of chunk PUTs must not re-handshake TLS each time.
     */
    private const val POOL_MAX_IDLE_CONNECTIONS = 8

    private const val POOL_KEEP_ALIVE_MINUTES = 10L

    private const val JSON_MEDIA_TYPE = "application/json"

    /**
     * `ignoreUnknownKeys` is the client half of "unknown fields are ignored by
     * both sides" (02-API.md §7.2); `explicitNulls = false` keeps absent optional
     * fields absent rather than sending `null`, which the fixture round-trip
     * asserts.
     */
    @Provides
    @Singleton
    fun provideJson(): Json = Json {
        ignoreUnknownKeys = true
        explicitNulls = false
        encodeDefaults = false
    }

    @Provides
    @Singleton
    fun provideConnectionPool(): ConnectionPool = ConnectionPool(
        POOL_MAX_IDLE_CONNECTIONS,
        POOL_KEEP_ALIVE_MINUTES,
        TimeUnit.MINUTES,
    )

    /** Read from the installed package rather than `BuildConfig`, so it always matches reality. */
    @Provides
    @Singleton
    fun provideClientVersion(@ApplicationContext context: Context): ClientVersion {
        val info = context.packageManager.getPackageInfo(context.packageName, 0)
        val versionCode = PackageInfoCompat.getLongVersionCode(info).toInt()
        return ClientVersion(versionCode, info.versionName.orEmpty())
    }

    @Provides
    @Singleton
    fun provideServerAddressSource(source: MutableServerAddressSource): ServerAddressSource = source

    @Provides
    @Singleton
    fun provideTokenStore(store: InMemoryTokenStore): TokenStore = store

    /**
     * The base client. HTTP/2 first (D-02: multiplexing is the biggest single
     * win for hundreds of small GETs), a 30 s call timeout, and the four
     * interceptors in the order documented on `Interceptors.kt`.
     *
     * [PinnedCallFactory] derives the pinned and the no-timeout transfer clients
     * from this one, so all three share this pool and dispatcher.
     */
    @Provides
    @Singleton
    fun provideOkHttpClient(
        connectionPool: ConnectionPool,
        serverAddress: ServerAddressInterceptor,
        clientVersion: ClientVersionInterceptor,
        auth: AuthInterceptor,
        latest: LatestVersionInterceptor,
    ): OkHttpClient = OkHttpClient.Builder()
        .connectionPool(connectionPool)
        .protocols(listOf(Protocol.HTTP_2, Protocol.HTTP_1_1))
        .callTimeout(CALL_TIMEOUT_SECONDS, TimeUnit.SECONDS)
        .connectTimeout(CONNECT_TIMEOUT_SECONDS, TimeUnit.SECONDS)
        .retryOnConnectionFailure(true)
        .addInterceptor(serverAddress)
        .addInterceptor(clientVersion)
        .addInterceptor(auth)
        .addInterceptor(latest)
        .build()

    @Provides
    @Singleton
    fun provideCallFactory(factory: PinnedCallFactory): Call.Factory = factory

    @Provides
    @Singleton
    fun provideRetrofit(callFactory: Call.Factory, json: Json): Retrofit = Retrofit.Builder()
        .baseUrl(ServerAddressInterceptor.PLACEHOLDER_BASE_URL)
        .callFactory(callFactory)
        .addConverterFactory(json.asConverterFactory(JSON_MEDIA_TYPE.toMediaType()))
        .build()

    @Provides
    @Singleton
    fun provideHomesinkApi(retrofit: Retrofit): HomesinkApi = retrofit.create(HomesinkApi::class.java)
}
