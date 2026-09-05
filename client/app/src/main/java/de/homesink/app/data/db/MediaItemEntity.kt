package de.homesink.app.data.db

import androidx.room.Entity
import androidx.room.Index
import androidx.room.PrimaryKey

/**
 * One local media file and its sync lifecycle. Mirrors `03-DATA-MODEL.md §2.1`
 * field-for-field.
 *
 * [state] is one of `de.homesink.app.contract.ItemState`'s names; [mode] is one
 * of `de.homesink.app.contract.SyncMode`'s names. Both are plain `String`
 * columns per the data model — the contract enums are the frozen vocabulary,
 * this entity just stores their names.
 */
@Entity(
    tableName = "media_item",
    indices = [
        Index(value = ["mediaStoreId"], unique = true),
        Index(value = ["state"]),
        Index(value = ["capturedAtMs"]),
    ],
)
data class MediaItemEntity(
    @PrimaryKey(autoGenerate = true) val id: Long = 0,
    val mediaStoreId: Long,
    val contentUri: String,
    val filename: String,
    val album: String,
    val mimeType: String,
    val mediaType: String,
    val sizeBytes: Long,
    val capturedAtMs: Long,
    val capturedOffsetMin: Int,
    val dateModifiedSec: Long,
    val width: Int?,
    val height: Int?,
    val durationMs: Long?,
    val hash: String?,
    val hashedAtMs: Long?,
    val state: String,
    val mode: String,
    val selected: Boolean = true,
    val remoteItemId: String?,
    val uploadedBytes: Long = 0,
    val attemptCount: Int = 0,
    val lastErrorCode: String?,
    val syncedAtMs: Long?,
    val runId: Long?,
)
