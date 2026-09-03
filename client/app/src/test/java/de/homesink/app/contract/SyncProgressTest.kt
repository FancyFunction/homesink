package de.homesink.app.contract

import org.junit.Assert.assertEquals
import org.junit.Test

/**
 * Guards the one piece of behaviour in the frozen `contract` package:
 * [SyncProgress.percent], including its divide-by-zero guard.
 */
class SyncProgressTest {

    @Test
    fun percentIsBytesBased() {
        val p = SyncProgress(doneFiles = 14, totalFiles = 47, sentBytes = 29, totalBytes = 100, currentFilename = null)
        assertEquals(29, p.percent)
    }

    @Test
    fun percentIsZeroWhenNothingQueued() {
        val p = SyncProgress(doneFiles = 0, totalFiles = 0, sentBytes = 0, totalBytes = 0, currentFilename = null)
        assertEquals(0, p.percent)
    }
}
