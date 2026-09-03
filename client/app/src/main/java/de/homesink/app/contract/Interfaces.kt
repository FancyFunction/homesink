package de.homesink.app.contract

import kotlinx.coroutines.flow.Flow

/**
 * FROZEN after WP-C1. The seams between client packages. Implementations live in
 * their owning package (`media/`, `data/net/`, `sync/`) and are bound by that
 * package's single Hilt module.
 */

/** Reads the device's media library (WP-C3). */
interface MediaScanner {
    /** Full scan of images, video and audio the app is permitted to see. */
    suspend fun scan(): List<LocalMedia>

    /** Emits once whenever the underlying `MediaStore` changes. */
    fun observe(): Flow<Unit>
}

/** Streams a file and returns its lowercase-hex SHA-256 (D-05, WP-C4). */
interface Hasher {
    suspend fun hash(uri: String): String
}

/**
 * The client half of the HTTP contract (02-API.md §4). Defined in full by WP-C5;
 * declared here only so packages that orchestrate sync can depend on the seam,
 * not the Retrofit implementation.
 */
interface SyncClient

/** Runs the batched, consent-gated local deletion (D-22, WP-C10). */
interface DeletionManager {
    suspend fun requestDeletion(ids: List<Long>): DeletionOutcome
}
