package de.homesink.app.ui.nav

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.NavigationBar
import androidx.compose.material3.NavigationBarItem
import androidx.compose.material3.NavigationBarItemDefaults
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.navigation.NavDestination.Companion.hierarchy
import androidx.navigation.NavGraph.Companion.findStartDestination
import androidx.navigation.NavHostController
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.currentBackStackEntryAsState
import androidx.navigation.compose.navigation
import androidx.navigation.compose.rememberNavController

/**
 * The app shell: a bottom navigation bar over a [NavHost] whose four children are
 * one nested graph per [Destination]. Switching tabs saves and restores each
 * graph's state, so the tabs keep independent back stacks and a rotation (state
 * restore) keeps the selected tab.
 */
@Composable
fun HomesinkNav(navController: NavHostController = rememberNavController()) {
    Scaffold(
        bottomBar = { HomesinkBottomBar(navController) },
    ) { innerPadding ->
        NavHost(
            navController = navController,
            startDestination = Destination.START.graphRoute,
            modifier = Modifier.padding(innerPadding),
        ) {
            Destination.entries.forEach { destination ->
                navigation(
                    startDestination = destination.homeRoute,
                    route = destination.graphRoute,
                ) {
                    composable(destination.homeRoute) { PlaceholderScreen(destination) }
                }
            }
        }
    }
}

@Composable
private fun HomesinkBottomBar(navController: NavHostController) {
    val backStackEntry by navController.currentBackStackEntryAsState()
    val currentHierarchy = backStackEntry?.destination?.hierarchy

    NavigationBar {
        Destination.entries.forEach { destination ->
            val selected = currentHierarchy?.any { it.route == destination.graphRoute } == true
            NavigationBarItem(
                selected = selected,
                onClick = {
                    navController.navigate(destination.graphRoute) {
                        popUpTo(navController.graph.findStartDestination().id) { saveState = true }
                        launchSingleTop = true
                        restoreState = true
                    }
                },
                icon = { Icon(destination.icon, contentDescription = null) },
                label = { Text(stringResource(destination.labelRes)) },
                colors = NavigationBarItemDefaults.colors(
                    // Chrome stays on primary tones; the accent is reserved for
                    // the sync action and progress fills (WP-C1 palette note).
                    indicatorColor = MaterialTheme.colorScheme.primaryContainer,
                    selectedIconColor = MaterialTheme.colorScheme.onPrimaryContainer,
                    selectedTextColor = MaterialTheme.colorScheme.onSurface,
                    unselectedIconColor = MaterialTheme.colorScheme.onSurfaceVariant,
                    unselectedTextColor = MaterialTheme.colorScheme.onSurfaceVariant,
                ),
            )
        }
    }
}

/** Empty per-tab screen — WP-C1 is the skeleton, so there is deliberately no content yet. */
@Composable
private fun PlaceholderScreen(destination: Destination) {
    Box(
        modifier = Modifier
            .fillMaxSize()
            .testTag(destination.screenTestTag),
        contentAlignment = Alignment.Center,
    ) {
        Text(
            text = stringResource(destination.labelRes),
            style = MaterialTheme.typography.headlineMedium,
        )
    }
}
