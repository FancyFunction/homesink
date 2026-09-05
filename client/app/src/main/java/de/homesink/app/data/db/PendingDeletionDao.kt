package de.homesink.app.data.db

import androidx.room.Dao
import androidx.room.Insert
import androidx.room.OnConflictStrategy
import androidx.room.Query
import kotlinx.coroutines.flow.Flow

/**
 * Queries against `pending_deletion` (D-22). Only the sync engine (WP-C8)
 * inserts here, and only the deletion manager (WP-C10) reads/clears it
 * (invariant C1) — this DAO just provides the storage both depend on.
 */
@Dao
interface PendingDeletionDao {

    @Insert(onConflict = OnConflictStrategy.REPLACE)
    suspend fun insert(deletion: PendingDeletionEntity): Long

    /** Everything still waiting on the batched consent dialog. */
    @Query("SELECT * FROM pending_deletion")
    fun observeAll(): Flow<List<PendingDeletionEntity>>

    /** Removes entries once the consent dialog has resolved them (deleted or kept). */
    @Query("DELETE FROM pending_deletion WHERE mediaStoreId IN (:mediaStoreIds)")
    suspend fun deleteByIds(mediaStoreIds: List<Long>): Int
}
