package de.homesink.app.data.db

import androidx.room.Dao
import androidx.room.Insert
import androidx.room.Query
import androidx.room.Update
import kotlinx.coroutines.flow.Flow

/** Queries against `sync_run`. */
@Dao
interface SyncRunDao {

    @Insert
    suspend fun insert(run: SyncRunEntity): Long

    /** Progress/finish updates as the engine reports them. */
    @Update
    suspend fun update(run: SyncRunEntity): Int

    /** For the queue screen (WP-C9) and settings (WP-C12), which show the current/last run. */
    @Query("SELECT * FROM sync_run ORDER BY id DESC LIMIT 1")
    fun observeLatest(): Flow<SyncRunEntity?>

    /** The `syncCurrentlyRunning` check in the notification decision rule (D-26, invariant C4). */
    @Query("SELECT * FROM sync_run WHERE state = 'RUNNING' LIMIT 1")
    suspend fun findRunning(): SyncRunEntity?
}
