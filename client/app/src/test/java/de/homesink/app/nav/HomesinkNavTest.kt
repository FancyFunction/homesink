package de.homesink.app.nav

import android.app.Application
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.hasClickAction
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import androidx.navigation.NavDestination.Companion.hierarchy
import androidx.navigation.compose.ComposeNavigator
import androidx.navigation.testing.TestNavHostController
import androidx.test.core.app.ApplicationProvider
import de.homesink.app.ui.nav.Destination
import de.homesink.app.ui.nav.HomesinkNav
import de.homesink.app.ui.theme.HomesinkTheme
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode

/**
 * Acceptance (WP-C1): "All four tabs navigate and keep their back stack
 * independently."
 */
@RunWith(RobolectricTestRunner::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(application = Application::class, sdk = [34])
class HomesinkNavTest {

    @get:Rule
    val composeRule = createComposeRule()

    private lateinit var navController: TestNavHostController

    private fun setContent() {
        composeRule.setContent {
            navController = TestNavHostController(ApplicationProvider.getApplicationContext()).apply {
                navigatorProvider.addNavigator(ComposeNavigator())
            }
            HomesinkTheme { HomesinkNav(navController) }
        }
        composeRule.waitForIdle()
    }

    private fun label(destination: Destination): String =
        ApplicationProvider.getApplicationContext<Application>().getString(destination.labelRes)

    /** The bottom-bar tab (which has a click action) — the label text alone also matches the screen headline. */
    private fun clickTab(destination: Destination) {
        composeRule.onNode(hasText(label(destination)) and hasClickAction()).performClick()
        composeRule.waitForIdle()
    }

    private fun currentRoute(): String? = navController.currentDestination?.route

    private fun inGraph(destination: Destination): Boolean =
        navController.currentDestination?.hierarchy?.any { it.route == destination.graphRoute } == true

    @Test
    fun startsOnSyncList() {
        setContent()
        assertEquals(Destination.SYNC_LIST.homeRoute, currentRoute())
        composeRule.onNodeWithTag(Destination.SYNC_LIST.screenTestTag).assertIsDisplayed()
    }

    @Test
    fun allFourTabsNavigate() {
        setContent()
        Destination.entries.forEach { destination ->
            clickTab(destination)
            assertEquals(
                "clicking ${destination.name} must land on its home route",
                destination.homeRoute,
                currentRoute(),
            )
            assertTrue("still inside ${destination.name}'s graph", inGraph(destination))
            composeRule.onNodeWithTag(destination.screenTestTag).assertIsDisplayed()
        }
    }

    @Test
    fun tabsKeepIndependentBackStacks() {
        setContent()

        // Visit three further tabs. Each switch pops to the saved start entry and
        // restores the target's own state, so tabs never pile onto each other.
        listOf(Destination.QUEUE, Destination.BROWSER, Destination.SETTINGS).forEach { destination ->
            clickTab(destination)
        }

        // Re-selecting an already-selected tab is a no-op (launchSingleTop).
        val beforeReselect = navController.currentBackStack.value.size
        clickTab(Destination.SETTINGS)
        assertEquals(
            "re-selecting the current tab must not grow the back stack",
            beforeReselect,
            navController.currentBackStack.value.size,
        )

        // Returning to a previously visited tab restores that tab, not a fresh copy.
        clickTab(Destination.QUEUE)
        assertEquals(Destination.QUEUE.homeRoute, currentRoute())

        // System back from any non-start tab returns to the start tab (SYNC_LIST),
        // because every tab switch kept only the saved start entry beneath it.
        composeRule.runOnIdle { navController.popBackStack() }
        composeRule.waitForIdle()
        assertEquals(Destination.SYNC_LIST.homeRoute, currentRoute())

        // And there is nothing stacked below the start tab.
        val poppedAgain = composeRule.runOnIdle { navController.popBackStack() }
        composeRule.waitForIdle()
        assertFalse("no destinations remain below the start tab", poppedAgain)
    }
}
