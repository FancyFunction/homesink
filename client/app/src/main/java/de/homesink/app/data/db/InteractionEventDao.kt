package de.homesink.app.data.db

import androidx.room.Dao
import androidx.room.Insert
import androidx.room.Query
import kotlinx.coroutines.flow.Flow

/** Queries against `interaction_event`, read by `ScheduleLearner` (WP-C11, `06-ALGORITHMS.md §1`). */
@Dao
interface InteractionEventDao {

    /** Records one D-27 event. */
    @Insert
    suspend fun insert(event: InteractionEventEntity): Long

    /**
     * The dataset `ScheduleLearner` folds into its weighted histogram: every
     * event no older than [sinceMs], oldest first. `ScheduleLearner` must stay
     * a pure function of `(events, now, settings)` (WP-C11 acceptance), so this
     * DAO hands back raw rows rather than a pre-aggregated score — the weighting,
     * decay and per-day/global split all happen in that pure function.
     */
    @Query("SELECT * FROM interaction_event WHERE timestampMs >= :sinceMs ORDER BY timestampMs ASC")
    fun recentEvents(sinceMs: Long): Flow<List<InteractionEventEntity>>

    /** `06-ALGORITHMS.md §1.2`: events older than 120 days are deleted. */
    @Query("DELETE FROM interaction_event WHERE timestampMs < :cutoffMs")
    suspend fun deleteOlderThan(cutoffMs: Long): Int
}
