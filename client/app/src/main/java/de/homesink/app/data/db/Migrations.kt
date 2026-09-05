package de.homesink.app.data.db

import androidx.room.migration.Migration

/**
 * Every schema change after version 1 gets a named `Migration` here, added to
 * [ALL] and never removed. Room's schema export (`app/schemas/`) keeps the
 * JSON snapshot each migration is tested against.
 *
 * Version 1 — the schema this work package creates — has no predecessor, so
 * there is nothing to migrate from yet.
 */
internal object Migrations {
    val ALL: Array<Migration> = emptyArray()
}
