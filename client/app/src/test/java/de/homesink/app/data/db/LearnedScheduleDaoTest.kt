package de.homesink.app.data.db

import android.app.Application
import androidx.room.Room
import androidx.test.core.app.ApplicationProvider
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.test.runTest
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/** WP-C2 acceptance: "in-memory DB test for every DAO method" (for [LearnedScheduleDao]). */
@RunWith(RobolectricTestRunner::class)
@Config(application = Application::class, sdk = [34])
class LearnedScheduleDaoTest {

    private lateinit var database: HomesinkDatabase
    private lateinit var dao: LearnedScheduleDao

    @Before
    fun createDatabase() {
        database = Room.inMemoryDatabaseBuilder(
            ApplicationProvider.getApplicationContext(),
            HomesinkDatabase::class.java,
        ).build()
        dao = database.learnedScheduleDao()
    }

    @After
    fun closeDatabase() {
        database.close()
    }

    private fun schedule(dayOfWeek: Int, minuteOfDay: Int = 1_320) = LearnedScheduleEntity(
        dayOfWeek = dayOfWeek,
        minuteOfDay = minuteOfDay,
        confidence = 0f,
        updatedAtMs = 0L,
    )

    @Test
    fun upsert_insertsANewDay() = runTest {
        dao.upsert(schedule(dayOfWeek = 1, minuteOfDay = 1_170))

        assertEquals(1_170, dao.findByDay(1)?.minuteOfDay)
    }

    @Test
    fun upsert_replacesAnExistingDayByPrimaryKey() = runTest {
        dao.upsert(schedule(dayOfWeek = 1, minuteOfDay = 1_320))
        dao.upsert(schedule(dayOfWeek = 1, minuteOfDay = 1_170))

        assertEquals(1_170, dao.findByDay(1)?.minuteOfDay)
    }

    @Test
    fun upsertAll_writesAllSevenRowsInOneCall() = runTest {
        dao.upsertAll((1..7).map { day -> schedule(dayOfWeek = day, minuteOfDay = 1_000 + day) })

        assertEquals(7, dao.observeAll().first().size)
    }

    @Test
    fun findByDay_returnsNullWhenNoScheduleLearnedYet() = runTest {
        assertNull(dao.findByDay(3))
    }

    @Test
    fun observeAll_ordersByDayOfWeek() = runTest {
        dao.upsertAll(listOf(schedule(dayOfWeek = 3), schedule(dayOfWeek = 1), schedule(dayOfWeek = 2)))

        assertEquals(listOf(1, 2, 3), dao.observeAll().first().map { it.dayOfWeek })
    }
}
