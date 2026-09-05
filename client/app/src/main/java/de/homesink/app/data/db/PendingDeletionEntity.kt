package de.homesink.app.data.db

import androidx.room.Entity
import androidx.room.PrimaryKey

/**
 * A local file whose upload was confirmed and that is now waiting on the
 * batched deletion consent dialog (D-22). Mirrors `03-DATA-MODEL.md §2.1`.
 *
 * This table is the only thing that survives the app being killed between a
 * confirmed upload and the consent dialog — [de.homesink.app.contract.DeletionManager]
 * reads it, and only the sync engine (WP-C8) writes to it (invariant C1).
 */
@Entity(tableName = "pending_deletion")
data class PendingDeletionEntity(
    @PrimaryKey val mediaStoreId: Long,
    val contentUri: String,
    val runId: Long,
    val confirmedUploadAtMs: Long,
)
