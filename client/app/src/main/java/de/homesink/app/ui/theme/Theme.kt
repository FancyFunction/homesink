package de.homesink.app.ui.theme

import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.material3.ColorScheme
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable

/**
 * Maps the requirement palette onto Material 3 roles exactly as the WP-C1 table
 * specifies. No dynamic colour: the brand palette is fixed.
 *
 * The accent ramp is bound to `secondary` and is used *only* for the primary
 * action (`Jetzt synchronisieren`) and progress fills — bottom-nav chrome is
 * driven from `primary`/`surface` tones (see `HomesinkNav`).
 */
internal val LightColors: ColorScheme = lightColorScheme(
    primary = Primary600,
    onPrimary = Primary100,
    primaryContainer = Primary200,
    onPrimaryContainer = Primary900,
    secondary = Accent500,
    onSecondary = Accent100,
    secondaryContainer = Primary200,
    onSecondaryContainer = Primary900,
    tertiary = Primary700,
    onTertiary = Primary100,
    background = Neutral100,
    onBackground = Neutral900,
    surface = Neutral100,
    onSurface = Neutral900,
    surfaceVariant = Neutral200,
    onSurfaceVariant = Neutral700,
    outline = Neutral400,
    outlineVariant = Neutral300,
    error = ErrorLight,
    onError = OnErrorLight,
)

internal val DarkColors: ColorScheme = darkColorScheme(
    primary = Primary400,
    onPrimary = Primary900,
    primaryContainer = Primary800,
    onPrimaryContainer = Primary100,
    secondary = Accent400,
    onSecondary = Accent900,
    secondaryContainer = Primary800,
    onSecondaryContainer = Primary100,
    tertiary = Primary300,
    onTertiary = Primary900,
    background = Neutral900,
    onBackground = Neutral200,
    surface = Neutral900,
    onSurface = Neutral200,
    surfaceVariant = Neutral800,
    onSurfaceVariant = Neutral300,
    outline = Neutral600,
    outlineVariant = Neutral700,
    error = ErrorDark,
    onError = OnErrorDark,
)

@Composable
fun HomesinkTheme(
    darkTheme: Boolean = isSystemInDarkTheme(),
    content: @Composable () -> Unit,
) {
    MaterialTheme(
        colorScheme = if (darkTheme) DarkColors else LightColors,
        typography = HomesinkTypography,
        content = content,
    )
}
