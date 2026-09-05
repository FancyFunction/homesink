package de.homesink.app.data.db

import android.app.Application
import androidx.room.Room
import androidx.test.core.app.ApplicationProvider
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.test.runTest
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/**
 * WP-C2 acceptance: "the interaction-event insert and the aggregate the
 * learner reads" — both covered here for [InteractionEventDao].
 */
@RunWith(RobolectricTestRunner::class)
@Config(application = Application::class, sdk = [34])
class InteractionEventDaoTest {

    private lateinit var database: HomesinkDatabase
    private lateinit var dao: InteractionEventDao

    @Before
    fun createDatabase() {
        database = Room.inMemoryDatabaseBuilder(
            ApplicationProvider.getApplicationContext(),
            HomesinkDatabase::class.java,
        ).build()
        dao = database.interactionEventDao()
    }

    @After
    fun closeDatabase() {
        database.close()
    }

    private fun event(timestampMs: Long, type: String = "SYNC_STARTED") = InteractionEventEntity(
        timestampMs = timestampMs,
        dayOfWeek = 1,
        minuteOfDay = 600,
        type = type,
    )

    @Test
    fun insert_storesTheEventsD27TypeIntact() = runTest {
        dao.insert(event(timestampMs = 1_000L, type = "NOTIFICATION_TAPPED"))

        val events = dao.recentEvents(sinceMs = 0L).first()

        assertEquals(1, events.size)
        assertEquals("NOTIFICATION_TAPPED", events.single().type)
    }

    @Test
    fun recentEvents_excludesEventsOlderThanSinceMs() = runTest {
        dao.insert(event(timestampMs = 1_000L))
        dao.insert(event(timestampMs = 5_000L))

        val recent = dao.recentEvents(sinceMs = 4_000L).first()

        assertEquals(1, recent.size)
        assertEquals(5_000L, recent.single().timestampMs)
    }

    @Test
    fun recentEvents_ordersOldestFirstForTheLearner() = runTest {
        dao.insert(event(timestampMs = 3_000L))
        dao.insert(event(timestampMs = 1_000L))
        dao.insert(event(timestampMs = 2_000L))

        val ordered = dao.recentEvents(sinceMs = 0L).first().map { it.timestampMs }

        assertEquals(listOf(1_000L, 2_000L, 3_000L), ordered)
    }

    @Test
    fun deleteOlderThan_removesOnlyEventsPastTheCutoff() = runTest {
        // 06-ALGORITHMS.md §1.2: events older than 120 days are deleted.
        val cutoffMs = 120L * 24 * 60 * 60 * 1000
        dao.insert(event(timestampMs = 0L))
        dao.insert(event(timestampMs = cutoffMs + 1))

        dao.deleteOlderThan(cutoffMs)

        val remaining = dao.recentEvents(sinceMs = 0L).first()
        assertEquals(1, remaining.size)
        assertEquals(cutoffMs + 1, remaining.single().timestampMs)
    }
}
