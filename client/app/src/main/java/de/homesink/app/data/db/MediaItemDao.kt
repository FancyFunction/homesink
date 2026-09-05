package de.homesink.app.data.db

import androidx.room.Dao
import androidx.room.Insert
import androidx.room.OnConflictStrategy
import androidx.room.Query
import androidx.room.Update
import kotlinx.coroutines.flow.Flow

/**
 * Queries against `media_item`. Business logic (which state transitions are
 * legal, how to merge a rescan) lives in the packages that call this DAO
 * (WP-C3/C4/C7/C8/C10) — this layer only stores and retrieves rows.
 */
@Dao
interface MediaItemDao {

    /** One-shot insert of a newly discovered file. Fails on a duplicate `mediaStoreId`. */
    @Insert(onConflict = OnConflictStrategy.ABORT)
    suspend fun insert(item: MediaItemEntity): Long

    /** Bulk insert for a scan batch. */
    @Insert(onConflict = OnConflictStrategy.ABORT)
    suspend fun insertAll(items: List<MediaItemEntity>): List<Long>

    /**
     * Replaces every column of an existing row, matched by [MediaItemEntity.id].
     * Used for every state/progress transition in the sync lifecycle
     * (`03-DATA-MODEL.md §2.2`) — hashing, preflight verdicts, upload progress
     * (invariant C2), commit results, and mode/selection edits (WP-C7).
     */
    @Update
    suspend fun update(item: MediaItemEntity): Int

    /** The dedupe/rescan cache lookup key is `(mediaStoreId, sizeBytes, dateModifiedSec)` (D-05, C3). */
    @Query("SELECT * FROM media_item WHERE mediaStoreId = :mediaStoreId")
    suspend fun findByMediaStoreId(mediaStoreId: Long): MediaItemEntity?

    /** Full-reconcile support for WP-C3: drops rows whose file no longer exists in `MediaStore`. */
    @Query("DELETE FROM media_item WHERE mediaStoreId NOT IN (:presentMediaStoreIds)")
    suspend fun deleteMissing(presentMediaStoreIds: List<Long>): Int

    /**
     * D-26's exact definition of PENDING: never successfully synced and not
     * deliberately skipped. Deselecting a file does not change its state, so
     * it keeps counting — that is intentional, not a bug in this query.
     */
    @Query("SELECT COUNT(*) FROM media_item WHERE state IN ($PENDING_STATES_SQL)")
    fun pendingCount(): Flow<Int>

    /**
     * D-26's `createdThisWeek`: PENDING files whose `capturedAtMs` falls in a
     * caller-supplied range (the current ISO week, Mon 00:00 inclusive to the
     * following Mon 00:00 exclusive, device-local — computed by the caller so
     * this stays a pure range query).
     */
    @Query(
        "SELECT COUNT(*) FROM media_item WHERE state IN ($PENDING_STATES_SQL) " +
            "AND capturedAtMs >= :weekStartMsInclusive AND capturedAtMs < :weekEndMsExclusive",
    )
    fun pendingCountCapturedInRange(weekStartMsInclusive: Long, weekEndMsExclusive: Long): Flow<Int>

    /** Paged items in any of [states], most recently captured first. */
    @Query(
        "SELECT * FROM media_item WHERE state IN (:states) " +
            "ORDER BY capturedAtMs DESC, id DESC LIMIT :limit OFFSET :offset",
    )
    fun observeByStates(states: List<String>, limit: Int, offset: Int): Flow<List<MediaItemEntity>>

    private companion object {
        /** D-26: PENDING = `state IN (DISCOVERED, HASHED, QUEUED, FAILED)`, exactly. */
        const val PENDING_STATES_SQL = "'DISCOVERED','HASHED','QUEUED','FAILED'"
    }
}
