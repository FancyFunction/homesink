package de.homesink.app.ui.theme

import androidx.compose.ui.graphics.Color

/**
 * The requirement's raw palette — all nine steps of each ramp as `Color` values
 * (05-WORKPACKAGES-CLIENT.md §WP-C1). The Material 3 role mapping lives in
 * [de.homesink.app.ui.theme.LightColors] / [DarkColors]; nothing outside this
 * package should reference a numbered step directly.
 */

// Primary — "Charybdis"
internal val Primary100 = Color(0xFFF2F9FF)
internal val Primary200 = Color(0xFFBCE5FE)
internal val Primary300 = Color(0xFF84D3F8)
internal val Primary400 = Color(0xFF4AC0E8)
internal val Primary500 = Color(0xFF16A6C9)
internal val Primary600 = Color(0xFF078BA1)
internal val Primary700 = Color(0xFF026E78)
internal val Primary800 = Color(0xFF004D4F)
internal val Primary900 = Color(0xFF002625)

// Accent — "End of Summer". Reserved for the primary action and progress fills.
internal val Accent100 = Color(0xFFFFFEF2)
internal val Accent200 = Color(0xFFFEF6BA)
internal val Accent300 = Color(0xFFF8E280)
internal val Accent400 = Color(0xFFE7C045)
internal val Accent500 = Color(0xFFC79010)
internal val Accent600 = Color(0xFF9F6805)
internal val Accent700 = Color(0xFF774601)
internal val Accent800 = Color(0xFF4E2900)
internal val Accent900 = Color(0xFF261200)

// Neutral
internal val Neutral100 = Color(0xFFFAFBFC)
internal val Neutral200 = Color(0xFFE5E9EB)
internal val Neutral300 = Color(0xFFD1D7DA)
internal val Neutral400 = Color(0xFFBEC6C9)
internal val Neutral500 = Color(0xFFABB5B8)
internal val Neutral600 = Color(0xFF889193)
internal val Neutral700 = Color(0xFF656E6F)
internal val Neutral800 = Color(0xFF444A4B)
internal val Neutral900 = Color(0xFF222626)

// Error — fixed hexes from the WP-C1 role table, not part of a ramp.
internal val ErrorLight = Color(0xFFB3261E)
internal val ErrorDark = Color(0xFFF2B8B5)
internal val OnErrorLight = Color(0xFFFFFFFF)
internal val OnErrorDark = Color(0xFF601410)
