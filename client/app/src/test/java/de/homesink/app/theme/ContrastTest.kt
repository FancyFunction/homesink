package de.homesink.app.theme

import androidx.compose.material3.ColorScheme
import androidx.compose.ui.graphics.Color
import de.homesink.app.ui.theme.DarkColors
import de.homesink.app.ui.theme.LightColors
import org.junit.Assert.assertTrue
import org.junit.Test
import kotlin.math.max
import kotlin.math.min
import kotlin.math.pow

/**
 * Acceptance (WP-C1): "light and dark both meet WCAG AA (4.5:1) for body text —
 * assert the computed contrast in a unit test, not by eye."
 *
 * Body text in Material 3 is `onSurface` drawn on `surface` (equivalently
 * `onBackground` on `background`). Both are asserted here against the sRGB WCAG 2
 * contrast formula.
 */
class ContrastTest {

    @Test
    fun lightBodyTextMeetsWcagAa() {
        assertAaBodyText(LightColors, "light")
    }

    @Test
    fun darkBodyTextMeetsWcagAa() {
        assertAaBodyText(DarkColors, "dark")
    }

    private fun assertAaBodyText(scheme: ColorScheme, name: String) {
        val onSurface = contrastRatio(scheme.onSurface, scheme.surface)
        val onBackground = contrastRatio(scheme.onBackground, scheme.background)
        assertTrue(
            "$name onSurface/surface contrast $onSurface must be >= 4.5:1",
            onSurface >= 4.5,
        )
        assertTrue(
            "$name onBackground/background contrast $onBackground must be >= 4.5:1",
            onBackground >= 4.5,
        )
    }

    private fun contrastRatio(a: Color, b: Color): Double {
        val la = relativeLuminance(a)
        val lb = relativeLuminance(b)
        return (max(la, lb) + 0.05) / (min(la, lb) + 0.05)
    }

    private fun relativeLuminance(c: Color): Double {
        fun channel(v: Float): Double {
            val d = v.toDouble()
            return if (d <= 0.03928) d / 12.92 else ((d + 0.055) / 1.055).pow(2.4)
        }
        return 0.2126 * channel(c.red) + 0.7152 * channel(c.green) + 0.0722 * channel(c.blue)
    }
}
