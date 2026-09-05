package de.homesink.app.data.db

import android.app.Application
import androidx.room.Room
import androidx.test.core.app.ApplicationProvider
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.test.runTest
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/** WP-C2 acceptance: "in-memory DB test for every DAO method" (for [PendingDeletionDao]). */
@RunWith(RobolectricTestRunner::class)
@Config(application = Application::class, sdk = [34])
class PendingDeletionDaoTest {

    private lateinit var database: HomesinkDatabase
    private lateinit var dao: PendingDeletionDao

    @Before
    fun createDatabase() {
        database = Room.inMemoryDatabaseBuilder(
            ApplicationProvider.getApplicationContext(),
            HomesinkDatabase::class.java,
        ).build()
        dao = database.pendingDeletionDao()
    }

    @After
    fun closeDatabase() {
        database.close()
    }

    private fun pending(mediaStoreId: Long, runId: Long = 1L) = PendingDeletionEntity(
        mediaStoreId = mediaStoreId,
        contentUri = "content://media/external/file/$mediaStoreId",
        runId = runId,
        confirmedUploadAtMs = 1_000L,
    )

    @Test
    fun insert_thenObserveAll_returnsIt() = runTest {
        dao.insert(pending(mediaStoreId = 1L))

        assertEquals(listOf(1L), dao.observeAll().first().map { it.mediaStoreId })
    }

    @Test
    fun insert_replacesAnExistingRowForTheSameMediaStoreId() = runTest {
        dao.insert(pending(mediaStoreId = 1L, runId = 1L))
        dao.insert(pending(mediaStoreId = 1L, runId = 2L))

        val all = dao.observeAll().first()
        assertEquals(1, all.size)
        assertEquals(2L, all.single().runId)
    }

    @Test
    fun observeAll_survivesAcrossDaoInstances() = runTest {
        // D-22: this table is what makes deletion survive the app being killed
        // before the consent dialog — a fresh DAO reading the same DB must see it.
        dao.insert(pending(mediaStoreId = 1L))

        val reopenedDao = database.pendingDeletionDao()

        assertEquals(listOf(1L), reopenedDao.observeAll().first().map { it.mediaStoreId })
    }

    @Test
    fun deleteByIds_removesOnlyTheResolvedEntries() = runTest {
        dao.insert(pending(mediaStoreId = 1L))
        dao.insert(pending(mediaStoreId = 2L))
        dao.insert(pending(mediaStoreId = 3L))

        dao.deleteByIds(listOf(1L, 3L))

        val remaining = dao.observeAll().first()
        assertEquals(1, remaining.size)
        assertTrue(remaining.any { it.mediaStoreId == 2L })
    }
}
