package de.homesink.app

import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import dagger.hilt.android.AndroidEntryPoint
import de.homesink.app.ui.nav.HomesinkNav
import de.homesink.app.ui.theme.HomesinkTheme

/**
 * The single Activity. New screens are reached through the `Destination` enum
 * (08-ROADMAP.md §4), never by adding activities.
 */
@AndroidEntryPoint
class MainActivity : ComponentActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()
        setContent {
            HomesinkTheme {
                HomesinkNav()
            }
        }
    }
}
