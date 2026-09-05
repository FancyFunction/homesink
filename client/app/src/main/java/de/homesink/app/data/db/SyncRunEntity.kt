package de.homesink.app.data.db

import androidx.room.Entity
import androidx.room.PrimaryKey

/**
 * One synchronisation run, from the moment the user (or the scheduler) starts
 * it to completion. Mirrors `03-DATA-MODEL.md §2.1`. At most one row may be in
 * `state = "RUNNING"` at a time (invariant C4, enforced by WP-C8's WorkManager
 * unique-work policy — this entity just stores the state).
 */
@Entity(tableName = "sync_run")
data class SyncRunEntity(
    @PrimaryKey(autoGenerate = true) val id: Long = 0,
    val startedAtMs: Long,
    val finishedAtMs: Long?,
    val totalFiles: Int,
    val doneFiles: Int,
    val failedFiles: Int,
    val totalBytes: Long,
    val sentBytes: Long,
    val state: String,
)
