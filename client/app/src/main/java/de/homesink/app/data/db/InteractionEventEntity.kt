package de.homesink.app.data.db

import androidx.room.Entity
import androidx.room.Index
import androidx.room.PrimaryKey

/**
 * One recorded interaction for the adaptive notification scheduler (D-27,
 * `06-ALGORITHMS.md §1`). Mirrors `03-DATA-MODEL.md §2.1`.
 *
 * [dayOfWeek] is a `java.time.DayOfWeek` value (1=Mon…7=Sun); [minuteOfDay] is
 * `0..1439`; [type] is one of the event names in D-27
 * (`NOTIFICATION_TAPPED`, `SYNC_STARTED`, `APP_OPENED_DIRECT`,
 * `NOTIFICATION_DISMISSED`, `NOTIFICATION_IGNORED`).
 */
@Entity(
    tableName = "interaction_event",
    indices = [Index(value = ["dayOfWeek", "minuteOfDay"])],
)
data class InteractionEventEntity(
    @PrimaryKey(autoGenerate = true) val id: Long = 0,
    val timestampMs: Long,
    val dayOfWeek: Int,
    val minuteOfDay: Int,
    val type: String,
)
