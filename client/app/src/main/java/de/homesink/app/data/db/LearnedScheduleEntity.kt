package de.homesink.app.data.db

import androidx.room.Entity
import androidx.room.PrimaryKey

/**
 * The learned notification time for one day of the week (`06-ALGORITHMS.md
 * §1.4`). Mirrors `03-DATA-MODEL.md §2.1`. The scheduler recomputes and
 * rewrites all seven rows daily at 03:00 local.
 */
@Entity(tableName = "learned_schedule")
data class LearnedScheduleEntity(
    @PrimaryKey val dayOfWeek: Int,
    val minuteOfDay: Int,
    val confidence: Float,
    val updatedAtMs: Long,
)
