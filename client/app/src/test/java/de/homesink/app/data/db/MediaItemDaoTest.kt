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

/**
 * WP-C2 acceptance: "in-memory DB test for every DAO method" (for [MediaItemDao])
 * and "`pendingCount()` matches the D-26 definition of PENDING exactly".
 */
@RunWith(RobolectricTestRunner::class)
@Config(application = Application::class, sdk = [34])
class MediaItemDaoTest {

    private lateinit var database: HomesinkDatabase
    private lateinit var dao: MediaItemDao

    @Before
    fun createDatabase() {
        database = Room.inMemoryDatabaseBuilder(
            ApplicationProvider.getApplicationContext(),
            HomesinkDatabase::class.java,
        ).build()
        dao = database.mediaItemDao()
    }

    @After
    fun closeDatabase() {
        database.close()
    }

    private fun item(
        mediaStoreId: Long = 1L,
        capturedAtMs: Long = 1_000L,
        state: String = "DISCOVERED",
    ) = MediaItemEntity(
        mediaStoreId = mediaStoreId,
        contentUri = "content://media/external/file/$mediaStoreId",
        filename = "IMG_$mediaStoreId.jpg",
        album = "Camera",
        mimeType = "image/jpeg",
        mediaType = "image",
        sizeBytes = 12_345L,
        capturedAtMs = capturedAtMs,
        capturedOffsetMin = 60,
        dateModifiedSec = 500L,
        width = 4000,
        height = 3000,
        durationMs = null,
        hash = null,
        hashedAtMs = null,
        state = state,
        mode = "UPLOAD",
        remoteItemId = null,
        lastErrorCode = null,
        syncedAtMs = null,
        runId = null,
    )

    @Test
    fun insert_thenFindByMediaStoreId_returnsTheStoredRow() = runTest {
        dao.insert(item(mediaStoreId = 42L, state = "HASHED"))

        val found = dao.findByMediaStoreId(42L)

        assertEquals("HASHED", found?.state)
        assertEquals(42L, found?.mediaStoreId)
    }

    @Test
    fun findByMediaStoreId_returnsNullWhenAbsent() = runTest {
        assertNull(dao.findByMediaStoreId(999L))
    }

    @Test
    fun insertAll_insertsEveryRow() = runTest {
        dao.insertAll(listOf(item(mediaStoreId = 1L), item(mediaStoreId = 2L), item(mediaStoreId = 3L)))

        assertEquals(3, dao.pendingCount().first())
    }

    @Test
    fun update_changesStateAndUploadedBytesByPrimaryKey() = runTest {
        val id = dao.insert(item(mediaStoreId = 7L, state = "QUEUED"))
        val stored = dao.findByMediaStoreId(7L)!!.copy(id = id)

        dao.update(stored.copy(state = "UPLOADING", uploadedBytes = 4_096L))

        val updated = dao.findByMediaStoreId(7L)
        assertEquals("UPLOADING", updated?.state)
        assertEquals(4_096L, updated?.uploadedBytes)
    }

    @Test
    fun deleteMissing_removesRowsNotInThePresentSet() = runTest {
        dao.insertAll(listOf(item(mediaStoreId = 1L), item(mediaStoreId = 2L), item(mediaStoreId = 3L)))

        dao.deleteMissing(presentMediaStoreIds = listOf(1L, 3L))

        assertNull(dao.findByMediaStoreId(2L))
        assertEquals(1L, dao.findByMediaStoreId(1L)?.mediaStoreId)
        assertEquals(3L, dao.findByMediaStoreId(3L)?.mediaStoreId)
    }

    @Test
    fun pendingCount_matchesD26PendingDefinitionExactly() = runTest {
        // D-26 / 03-DATA-MODEL.md §2.2: PENDING = DISCOVERED, HASHED, QUEUED, FAILED.
        // Every other state — including SKIPPED, which is easy to mistake for
        // "not yet synced" — must NOT count.
        dao.insertAll(
            listOf(
                item(mediaStoreId = 1L, state = "DISCOVERED"),
                item(mediaStoreId = 2L, state = "HASHED"),
                item(mediaStoreId = 3L, state = "QUEUED"),
                item(mediaStoreId = 4L, state = "FAILED"),
                item(mediaStoreId = 5L, state = "UPLOADING"),
                item(mediaStoreId = 6L, state = "SYNCED"),
                item(mediaStoreId = 7L, state = "SYNCED_DELETED"),
                item(mediaStoreId = 8L, state = "SYNCED_KEPT"),
                item(mediaStoreId = 9L, state = "SKIPPED"),
            ),
        )

        assertEquals(4, dao.pendingCount().first())
    }

    @Test
    fun pendingCount_deselectingAFileDoesNotChangeWhetherItCounts() = runTest {
        // 03-DATA-MODEL.md §2.2: "Deselecting a file does not change its state,
        // so it keeps counting; that is intentional."
        val id = dao.insert(item(mediaStoreId = 1L, state = "DISCOVERED"))
        val stored = dao.findByMediaStoreId(1L)!!.copy(id = id)

        dao.update(stored.copy(selected = false))

        assertEquals(1, dao.pendingCount().first())
    }

    @Test
    fun pendingCountCapturedInRange_countsOnlyPendingFilesInsideTheWindow() = runTest {
        dao.insertAll(
            listOf(
                item(mediaStoreId = 1L, capturedAtMs = 500L, state = "DISCOVERED"), // before window
                item(mediaStoreId = 2L, capturedAtMs = 1_000L, state = "HASHED"), // start, inclusive
                item(mediaStoreId = 3L, capturedAtMs = 1_500L, state = "QUEUED"), // inside
                item(mediaStoreId = 4L, capturedAtMs = 2_000L, state = "FAILED"), // end, exclusive
                item(mediaStoreId = 5L, capturedAtMs = 1_500L, state = "SYNCED"), // inside window, not pending
            ),
        )

        val count = dao.pendingCountCapturedInRange(weekStartMsInclusive = 1_000L, weekEndMsExclusive = 2_000L).first()

        assertEquals(2, count)
    }

    @Test
    fun observeByStates_returnsOnlyRequestedStatesNewestFirst() = runTest {
        dao.insertAll(
            listOf(
                item(mediaStoreId = 1L, capturedAtMs = 1_000L, state = "QUEUED"),
                item(mediaStoreId = 2L, capturedAtMs = 3_000L, state = "QUEUED"),
                item(mediaStoreId = 3L, capturedAtMs = 2_000L, state = "UPLOADING"),
                item(mediaStoreId = 4L, capturedAtMs = 4_000L, state = "SYNCED"),
            ),
        )

        val page = dao.observeByStates(states = listOf("QUEUED", "UPLOADING"), limit = 10, offset = 0).first()

        assertEquals(listOf(2L, 3L, 1L), page.map { it.mediaStoreId })
    }

    @Test
    fun observeByStates_respectsLimitAndOffsetForPaging() = runTest {
        dao.insertAll(
            (1..5L).map { id -> item(mediaStoreId = id, capturedAtMs = id * 1_000L, state = "QUEUED") },
        )

        val secondPage = dao.observeByStates(states = listOf("QUEUED"), limit = 2, offset = 2).first()

        // Newest-first order is 5,4,3,2,1 — offset 2, limit 2 selects 3 and 2.
        assertEquals(listOf(3L, 2L), secondPage.map { it.mediaStoreId })
    }
}
