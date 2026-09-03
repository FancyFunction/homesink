package de.homesink.app.nav

import android.app.Application
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.junit4.StateRestorationTester
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.test.core.app.ApplicationProvider
import de.homesink.app.ui.nav.Destination
import de.homesink.app.ui.nav.HomesinkNav
import de.homesink.app.ui.theme.HomesinkTheme
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode

/**
 * Acceptance (WP-C1): "rotation preserves the selected tab."
 *
 * A rotation is a configuration change: the composition is torn down and rebuilt
 * with the saved instance state. [StateRestorationTester] emulates exactly that.
 */
@RunWith(RobolectricTestRunner::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(application = Application::class, sdk = [34])
class RotationTest {

    @get:Rule
    val composeRule = createComposeRule()

    private fun label(destination: Destination): String =
        ApplicationProvider.getApplicationContext<Application>().getString(destination.labelRes)

    @Test
    fun selectedTabSurvivesConfigurationChange() {
        val restorationTester = StateRestorationTester(composeRule)
        restorationTester.setContent {
            HomesinkTheme { HomesinkNav() }
        }

        composeRule.onNodeWithText(label(Destination.SETTINGS)).performClick()
        composeRule.waitForIdle()
        composeRule.onNodeWithTag(Destination.SETTINGS.screenTestTag).assertIsDisplayed()

        restorationTester.emulateSavedInstanceStateRestore()
        composeRule.waitForIdle()

        composeRule.onNodeWithTag(Destination.SETTINGS.screenTestTag).assertIsDisplayed()
    }
}
