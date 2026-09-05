package de.homesink.app.data.db

import androidx.room.Database
import androidx.room.RoomDatabase

/**
 * The client's Room database (`03-DATA-MODEL.md §2`). Schema exported to
 * `app/schemas/` per the WP-C2 acceptance criterion; bump [Database.version]
 * and add a [Migrations] entry for every future change — never edit an
 * already-shipped schema in place.
 */
@Database(
    entities = [
        MediaItemEntity::class,
        SyncRunEntity::class,
        InteractionEventEntity::class,
        LearnedScheduleEntity::class,
        PendingDeletionEntity::class,
    ],
    version = 1,
    exportSchema = true,
)
abstract class HomesinkDatabase : RoomDatabase() {
    abstract fun mediaItemDao(): MediaItemDao
    abstract fun syncRunDao(): SyncRunDao
    abstract fun interactionEventDao(): InteractionEventDao
    abstract fun learnedScheduleDao(): LearnedScheduleDao
    abstract fun pendingDeletionDao(): PendingDeletionDao

    companion object {
        const val DATABASE_NAME = "homesink.db"
    }
}
