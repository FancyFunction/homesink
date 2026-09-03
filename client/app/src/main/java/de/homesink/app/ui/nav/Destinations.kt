package de.homesink.app.ui.nav

import androidx.annotation.StringRes
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.List
import androidx.compose.material.icons.filled.Home
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material.icons.filled.Settings
import androidx.compose.ui.graphics.vector.ImageVector
import de.homesink.app.R

/**
 * The four bottom-navigation destinations (requirement: "a navigation bar at the
 * bottom that changes the rest of the display area").
 *
 * This enum is the **only** place a screen is added (08-ROADMAP.md §4). Each
 * entry owns a nested nav graph so its back stack is independent of the others'.
 */
enum class Destination(
    val route: String,
    @param:StringRes val labelRes: Int,
    val icon: ImageVector,
) {
    SYNC_LIST("sync_list", R.string.nav_sync_list, Icons.Filled.Refresh),
    BROWSER("browser", R.string.nav_browser, Icons.Filled.Home),
    QUEUE("queue", R.string.nav_queue, Icons.AutoMirrored.Filled.List),
    SETTINGS("settings", R.string.nav_settings, Icons.Filled.Settings),
    ;

    /** Route of this tab's nested graph — the unit that carries an independent back stack. */
    val graphRoute: String get() = "$route/graph"

    /** Route of the tab's start screen inside its graph. */
    val homeRoute: String get() = "$route/home"

    /** Compose test tag for the tab's screen container. */
    val screenTestTag: String get() = "screen/$route"

    companion object {
        /** Destination shown on cold start. */
        val START: Destination = SYNC_LIST
    }
}
