package de.homesink.app.contract

/**
 * FROZEN after WP-C1 (08-ROADMAP.md §4): pure Kotlin, no Android imports. Later
 * client packages may read this file but never edit it. Every downstream layer
 * (db, net, media, sync, ui) speaks in these types.
 */

/** Broad kind of a local media file (D-14 allowlist collapses to these three). */
enum class MediaType { IMAGE, VIDEO, AUDIO }

/**
 * What the user chose for a file on the synchronisation list.
 *
 * `UPLOAD_AND_DELETE` means the local original is removed *after* a confirmed
 * server success (requirement; client invariant C1, D-07).
 */
enum class SyncMode { UPLOAD, UPLOAD_AND_DELETE }

/**
 * Lifecycle of one local media file. Mirrors the `media_item.state` machine in
 * 03-DATA-MODEL.md §2.2.
 *
 * `PENDING` in the notification rule (D-26) is the set
 * `{DISCOVERED, HASHED, QUEUED, FAILED}` — not a value here.
 */
enum class ItemState {
    DISCOVERED,
    HASHED,
    QUEUED,
    UPLOADING,
    SYNCED,
    SYNCED_DELETED,
    SYNCED_KEPT,
    FAILED,
    SKIPPED,
}

/**
 * One media file discovered on the device.
 *
 * @param id local database id (0 until persisted).
 * @param mediaStoreId `MediaStore._ID`, the join key back to the OS.
 * @param uri content URI as a string (kept as `String` so this type stays free of Android imports).
 * @param album raw `BUCKET_DISPLAY_NAME` (D-09); the server sanitises it (D-10).
 * @param capturedAtMs capture instant, epoch millis (D-12 fallback chain).
 * @param capturedOffsetMin UTC offset in minutes at the capture instant (D-11).
 * @param durationMs playback duration for video/audio, `null` for images.
 * @param hash lowercase-hex SHA-256 of the whole file (D-05), `null` until hashed.
 */
data class LocalMedia(
    val id: Long,
    val mediaStoreId: Long,
    val uri: String,
    val filename: String,
    val album: String,
    val mimeType: String,
    val mediaType: MediaType,
    val sizeBytes: Long,
    val capturedAtMs: Long,
    val capturedOffsetMin: Int,
    val durationMs: Long?,
    val hash: String?,
    val state: ItemState,
    val mode: SyncMode,
    val selected: Boolean,
)

/**
 * Progress of a running synchronisation. Emitted at most twice a second by the
 * sync engine (WP-C8); rendered by the progress notification (WP-C9) as
 * `sync_progress_fmt`.
 */
data class SyncProgress(
    val doneFiles: Int,
    val totalFiles: Int,
    val sentBytes: Long,
    val totalBytes: Long,
    val currentFilename: String?,
) {
    /** Whole-percent of bytes sent; 0 when nothing is queued. */
    val percent: Int
        get() = if (totalBytes == 0L) 0 else ((sentBytes * 100) / totalBytes).toInt()
}

/**
 * Result of a batched local-deletion request (D-22). `ids` are `MediaStore._ID`
 * values, the same identifiers passed to [DeletionManager.requestDeletion].
 */
sealed interface DeletionOutcome {

    /**
     * The system delete request ran to completion.
     *
     * @param deletedIds files the OS actually removed.
     * @param keptIds files the user spared in the consent dialog — marked
     *   `SYNCED_KEPT` and never re-offered (D-22).
     */
    data class Completed(
        val deletedIds: List<Long>,
        val keptIds: List<Long>,
    ) : DeletionOutcome

    /** The user dismissed the consent dialog without deleting anything. */
    data class Declined(val ids: List<Long>) : DeletionOutcome

    /**
     * The app was not in the foreground, so an `IntentSender` could not be
     * launched (D-22). Deletion is deferred; the ids stay in `pending_deletion`
     * and a `notif_ready_to_delete` notification is posted.
     */
    data class Deferred(val ids: List<Long>) : DeletionOutcome
}
