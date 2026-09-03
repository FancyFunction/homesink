package de.homesink.app

import android.app.Application
import dagger.hilt.android.HiltAndroidApp

/**
 * Process entry point and Hilt's `SingletonComponent` root. Feature packages each
 * contribute exactly one Hilt module (00-ARCHITECTURE.md §4.1); WP-C1 adds none.
 */
@HiltAndroidApp
class HomesinkApp : Application()
