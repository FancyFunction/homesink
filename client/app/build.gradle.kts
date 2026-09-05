plugins {
    alias(libs.plugins.android.application)
    alias(libs.plugins.kotlin.android)
    alias(libs.plugins.kotlin.compose)
    alias(libs.plugins.kotlin.serialization)
    alias(libs.plugins.ksp)
    alias(libs.plugins.hilt)
}

android {
    namespace = "de.homesink.app"
    compileSdk = 36

    defaultConfig {
        applicationId = "de.homesink.app"
        minSdk = 26
        targetSdk = 36
        versionCode = 1
        versionName = "0.1.0"
        testInstrumentationRunner = "androidx.test.runner.AndroidJUnitRunner"
    }

    buildTypes {
        release {
            isMinifyEnabled = false
            proguardFiles(
                getDefaultProguardFile("proguard-android-optimize.txt"),
                "proguard-rules.pro",
            )
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
    kotlinOptions {
        jvmTarget = "17"
    }
    buildFeatures {
        compose = true
    }
    lint {
        warningsAsErrors = true
        abortOnError = true
        checkDependencies = true
        // "Newer version available" checks are relative to the sandbox's lint
        // database, not a real defect: dependency versions are pinned on purpose
        // and WP-C1 owns the version catalog's coherence (08-ROADMAP.md §4).
        disable += setOf("GradleDependency", "NewerVersionAvailable", "AndroidGradlePluginVersion")
        // Adaptive icons must live in mipmap-anydpi-v26 (AAPT requires the -v26
        // qualifier for <adaptive-icon>), which ObsoleteSdkInt flags as redundant
        // at minSdk 26. The qualifier is load-bearing here, so silence that one.
        disable += "ObsoleteSdkInt"
    }
    testOptions {
        unitTests {
            isIncludeAndroidResources = true
            isReturnDefaultValues = true
        }
    }
    packaging {
        resources.excludes += "/META-INF/{AL2.0,LGPL2.1}"
    }
}

// WP-C2 (08-ROADMAP.md §4: dependency additions go through WP-C1's build files):
// export the Room schema to app/schemas/ per 03-DATA-MODEL.md §2, one JSON file
// per database version, committed so a later migration can be tested against it.
ksp {
    arg("room.schemaLocation", "$projectDir/schemas")
    arg("room.generateKotlin", "true")
}

// Unit tests run against the debug variant only. The Robolectric + Compose UI
// tests need the debug-only `ui-test-manifest` (it contributes the host
// ComponentActivity); there is nothing release-specific for WP-C1 to test.
androidComponents {
    beforeVariants(selector().withBuildType("release")) { variantBuilder ->
        @Suppress("UnstableApiUsage")
        variantBuilder.enableUnitTest = false
    }
}

// 08-ROADMAP.md §5: once the client module exists, the shared contract fixtures
// are copied in by a Gradle task instead of the standalone script. Later work
// packages (WP-C5) deserialise the same bytes the Go contract tests use.
val syncFixtures by tasks.registering(Exec::class) {
    description = "Copies the shared contract fixtures from backend/internal/testutil/testdata."
    group = "verification"
    workingDir = rootProject.projectDir.parentFile
    commandLine("scripts/sync-fixtures.sh")
    isIgnoreExitValue = true
    doLast {
        val result = executionResult.get()
        if (result.exitValue != 0) {
            logger.warn("syncFixtures: scripts/sync-fixtures.sh exited ${result.exitValue}; " +
                "contract fixtures may be stale (harmless for WP-C1).")
        }
    }
}
tasks.withType<Test>().configureEach { dependsOn(syncFixtures) }

// Sideload helpers. AGP already contributes `:app:installDebug` /
// `:app:installRelease` (they build the variant and run `adb install` against
// the connected device, handling device selection and split APKs). These are
// memorable aliases plus a launch step.
val installApk by tasks.registering {
    group = "install"
    description = "Installs the debug APK on the connected Android device (alias for installDebug)."
    dependsOn("installDebug")
}

tasks.register<Exec>("launchApp") {
    group = "install"
    description = "Installs the debug APK, then starts MainActivity on the connected device."
    dependsOn(installApk)
    val adb = android.sdkDirectory.resolve("platform-tools/adb").absolutePath
    val component = "${android.defaultConfig.applicationId}/de.homesink.app.MainActivity"
    commandLine(adb, "shell", "am", "start", "-n", component)
}

dependencies {
    implementation(libs.androidx.core.ktx)
    implementation(libs.androidx.lifecycle.runtime.ktx)
    implementation(libs.androidx.lifecycle.viewmodel.compose)
    implementation(libs.androidx.activity.compose)
    implementation(platform(libs.androidx.compose.bom))
    implementation(libs.androidx.ui)
    implementation(libs.androidx.ui.graphics)
    implementation(libs.androidx.ui.tooling.preview)
    implementation(libs.androidx.material3)
    implementation(libs.androidx.material.icons.core)
    implementation(libs.androidx.navigation.compose)
    implementation(libs.kotlinx.coroutines.core)

    implementation(libs.hilt.android)
    ksp(libs.hilt.compiler)

    implementation(libs.androidx.room.runtime)
    implementation(libs.androidx.room.ktx)
    ksp(libs.androidx.room.compiler)

    // WP-C5 network layer (05-WORKPACKAGES-CLIENT.md): OkHttp 4 + Retrofit 2 +
    // kotlinx-serialization. Added here because 08-ROADMAP.md §4 routes every
    // dependency addition through WP-C1's build files and version catalog.
    implementation(libs.kotlinx.serialization.json)
    implementation(libs.okhttp)
    implementation(libs.retrofit)
    implementation(libs.retrofit.kotlinx.serialization)

    debugImplementation(libs.androidx.ui.tooling)
    debugImplementation(libs.androidx.ui.test.manifest)

    testImplementation(libs.junit)
    testImplementation(libs.robolectric)
    testImplementation(libs.androidx.test.ext.junit)
    testImplementation(libs.androidx.test.core)
    testImplementation(libs.kotlinx.coroutines.test)
    testImplementation(platform(libs.androidx.compose.bom))
    testImplementation(libs.androidx.ui.test.junit4)
    testImplementation(libs.androidx.navigation.testing)
    testImplementation(libs.okhttp.mockwebserver)
    testImplementation(libs.okhttp.tls)

    androidTestImplementation(platform(libs.androidx.compose.bom))
    androidTestImplementation(libs.androidx.ui.test.junit4)
    androidTestImplementation(libs.androidx.test.ext.junit)
}
