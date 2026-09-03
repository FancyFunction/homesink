// Root build script. Plugins are declared here (apply false) and applied in :app
// so the single-module setup from 00-ARCHITECTURE.md §4.1 stays intentional.
plugins {
    alias(libs.plugins.android.application) apply false
    alias(libs.plugins.kotlin.android) apply false
    alias(libs.plugins.kotlin.compose) apply false
    alias(libs.plugins.ksp) apply false
    alias(libs.plugins.hilt) apply false
}
