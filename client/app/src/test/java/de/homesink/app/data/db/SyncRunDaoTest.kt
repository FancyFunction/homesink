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

/** WP-C2 acceptance: "in-memory DB test for every DAO method" (for [SyncRunDao]). */
@RunWith(RobolectricTestRunner::class)
@Config(application = Application::class, sdk = [34])
class SyncRunDaoTest {

    private lateinit var database: HomesinkDatabase
    private lateinit var dao: SyncRunDao

    @Before
    fun createDatabase() {
        database = Room.inMemoryDatabaseBuilder(
            ApplicationProvider.getApplicationContext(),
            HomesinkDatabase::class.java,
        ).build()
        dao = database.syncRunDao()
    }

    @After
    fun closeDatabase() {
        database.close()
    }

    private fun run(state: String, startedAtMs: Long = 0L) = SyncRunEntity(
        startedAtMs = startedAtMs,
        finishedAtMs = null,
        totalFiles = 10,
        doneFiles = 0,
        failedFiles = 0,
        totalBytes = 1_000L,
        sentBytes = 0L,
        state = state,
    )

    @Test
    fun insert_thenObserveLatest_returnsIt() = runTest {
        dao.insert(run(state = "RUNNING", startedAtMs = 1_000L))

        assertEquals("RUNNING", dao.observeLatest().first()?.state)
    }

    @Test
    fun observeLatest_returnsNullWhenNoRunExists() = runTest {
        assertNull(dao.observeLatest().first())
    }

    @Test
    fun observeLatest_returnsTheMostRecentlyInsertedRun() = runTest {
        dao.insert(run(state = "SUCCESS", startedAtMs = 1_000L))
        dao.insert(run(state = "RUNNING", startedAtMs = 2_000L))

        assertEquals("RUNNING", dao.observeLatest().first()?.state)
    }

    @Test
    fun update_changesProgressByPrimaryKey() = runTest {
        val id = dao.insert(run(state = "RUNNING"))
        val stored = dao.observeLatest().first()!!.copy(id = id)

        dao.update(stored.copy(doneFiles = 4, sentBytes = 400L))

        val updated = dao.observeLatest().first()
        assertEquals(4, updated?.doneFiles)
        assertEquals(400L, updated?.sentBytes)
    }

    @Test
    fun findRunning_returnsTheRunningRowForTheSyncCurrentlyRunningCheck() = runTest {
        dao.insert(run(state = "SUCCESS"))
        dao.insert(run(state = "RUNNING"))

        assertEquals("RUNNING", dao.findRunning()?.state)
    }

    @Test
    fun findRunning_returnsNullWhenNothingIsRunning() = runTest {
        dao.insert(run(state = "SUCCESS"))
        dao.insert(run(state = "FAILED"))

        assertNull(dao.findRunning())
    }
}
