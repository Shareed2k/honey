package com.honey.mobile.ui.theme

import android.app.Activity
import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.SideEffect
import androidx.compose.ui.graphics.toArgb
import androidx.compose.ui.platform.LocalView
import androidx.core.view.WindowCompat

private val DarkColors = darkColorScheme(
    primary = NeonCyan,
    onPrimary = OnNeon,
    primaryContainer = NeonCyanDim,
    onPrimaryContainer = TextHi,
    secondary = NeonViolet,
    onSecondary = OnNeon,
    secondaryContainer = NeonVioletDim,
    onSecondaryContainer = TextHi,
    tertiary = NeonGreen,
    onTertiary = OnNeon,
    background = BgBlack,
    onBackground = TextHi,
    surface = BgPanel,
    onSurface = TextHi,
    surfaceVariant = BgPanelHi,
    onSurfaceVariant = TextMid,
    outline = BgOutline,
    outlineVariant = BgOutline,
    error = NeonRed,
    onError = OnNeon,
)

private val LightColors = lightColorScheme(
    primary = NeonCyanDim,
    onPrimary = LightPanel,
    secondary = NeonVioletDim,
    onSecondary = LightPanel,
    background = LightBg,
    onBackground = LightText,
    surface = LightPanel,
    onSurface = LightText,
    surfaceVariant = LightBg,
    onSurfaceVariant = TextDim,
    outline = LightOutline,
    error = NeonRed,
)

/**
 * HoneyTheme — dark-first neon/cyber Material3 theme. No dynamic color: brand
 * identity is intentional. Honors system dark mode but biases dark.
 */
@Composable
fun HoneyTheme(
    darkTheme: Boolean = true || isSystemInDarkTheme(),
    content: @Composable () -> Unit,
) {
    val colors = if (darkTheme) DarkColors else LightColors
    val view = LocalView.current
    if (!view.isInEditMode) {
        SideEffect {
            val window = (view.context as Activity).window
            WindowCompat.setDecorFitsSystemWindows(window, false)
            window.statusBarColor = android.graphics.Color.TRANSPARENT
            window.navigationBarColor = android.graphics.Color.TRANSPARENT
            val controller = WindowCompat.getInsetsController(window, view)
            controller.isAppearanceLightStatusBars = !darkTheme
            controller.isAppearanceLightNavigationBars = !darkTheme
        }
    }
    MaterialTheme(
        colorScheme = colors,
        typography = HoneyTypography,
        shapes = HoneyShapes,
        content = content,
    )
}
