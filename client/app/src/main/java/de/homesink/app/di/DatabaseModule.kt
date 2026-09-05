package de.homesink.app.di

import android.content.Context
import androidx.room.Room
import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.android.qualifiers.ApplicationContext
import dagger.hilt.components.SingletonComponent
import de.homesink.app.data.db.HomesinkDatabase
import de.homesink.app.data.db.InteractionEventDao
import de.homesink.app.data.db.LearnedScheduleDao
import de.homesink.app.data.db.MediaItemDao
import de.homesink.app.data.db.Migrations
import de.homesink.app.data.db.PendingDeletionDao
import de.homesink.app.data.db.SyncRunDao
import javax.inject.Singleton

/**
 * One Hilt module for the `data/db` feature package (00-ARCHITECTURE.md §4.1).
 * The database itself is the only binding that needs a builder; every DAO is
 * a thin accessor off the singleton instance.
 */
@Module
@InstallIn(SingletonComponent::class)
object DatabaseModule {

    @Provides
    @Singleton
    fun provideHomesinkDatabase(@ApplicationContext context: Context): HomesinkDatabase =
        Room.databaseBuilder(context, HomesinkDatabase::class.java, HomesinkDatabase.DATABASE_NAME)
            .addMigrations(*Migrations.ALL)
            .build()

    @Provides
    fun provideMediaItemDao(database: HomesinkDatabase): MediaItemDao = database.mediaItemDao()

    @Provides
    fun provideSyncRunDao(database: HomesinkDatabase): SyncRunDao = database.syncRunDao()

    @Provides
    fun provideInteractionEventDao(database: HomesinkDatabase): InteractionEventDao =
        database.interactionEventDao()

    @Provides
    fun provideLearnedScheduleDao(database: HomesinkDatabase): LearnedScheduleDao =
        database.learnedScheduleDao()

    @Provides
    fun providePendingDeletionDao(database: HomesinkDatabase): PendingDeletionDao =
        database.pendingDeletionDao()
}
