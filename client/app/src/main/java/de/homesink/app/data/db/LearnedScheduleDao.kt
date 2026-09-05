package de.homesink.app.data.db

import androidx.room.Dao
import androidx.room.Insert
import androidx.room.OnConflictStrategy
import androidx.room.Query
import kotlinx.coroutines.flow.Flow

/** Queries against `learned_schedule` (`06-ALGORITHMS.md §1.4`). */
@Dao
interface LearnedScheduleDao {

    /** The daily recomputation rewrites one day's row. */
    @Insert(onConflict = OnConflictStrategy.REPLACE)
    suspend fun upsert(schedule: LearnedScheduleEntity): Long

    /** The daily recomputation writes all seven rows at once (`06-ALGORITHMS.md §1.4`). */
    @Insert(onConflict = OnConflictStrategy.REPLACE)
    suspend fun upsertAll(schedules: List<LearnedScheduleEntity>): List<Long>

    /** Read by Settings (WP-C12) and the scheduler's daily-decision rule (`06-ALGORITHMS.md §1.5`). */
    @Query("SELECT * FROM learned_schedule ORDER BY dayOfWeek")
    fun observeAll(): Flow<List<LearnedScheduleEntity>>

    /** The hysteresis step needs the previous slot for day [dayOfWeek] (`06-ALGORITHMS.md §1.4` step 7). */
    @Query("SELECT * FROM learned_schedule WHERE dayOfWeek = :dayOfWeek")
    suspend fun findByDay(dayOfWeek: Int): LearnedScheduleEntity?
}
