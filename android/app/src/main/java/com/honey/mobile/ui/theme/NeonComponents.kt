package com.honey.mobile.ui.theme

import androidx.compose.animation.animateColorAsState
import androidx.compose.animation.core.FastOutSlowInEasing
import androidx.compose.animation.core.LinearEasing
import androidx.compose.animation.core.RepeatMode
import androidx.compose.animation.core.animateFloat
import androidx.compose.animation.core.infiniteRepeatable
import androidx.compose.animation.core.rememberInfiniteTransition
import androidx.compose.animation.core.tween
import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp

/** GradientBackground fills its content area with a deep radial cyan→violet→bg wash. */
@Composable
fun GradientBackground(
    modifier: Modifier = Modifier,
    content: @Composable () -> Unit,
) {
    Box(
        modifier = modifier
            .fillMaxSize()
            .background(BgBlack)
            .drawBehind {
                drawRect(
                    brush = Brush.radialGradient(
                        colors = listOf(
                            NeonViolet.copy(alpha = 0.16f),
                            NeonCyan.copy(alpha = 0.06f),
                            Color.Transparent,
                        ),
                        center = Offset(size.width * 0.5f, size.height * 0.18f),
                        radius = size.maxDimension * 0.9f,
                    ),
                )
            },
    ) { content() }
}

/** GlowCard is a surface card with a subtle gradient border + outer neon glow. */
@Composable
fun GlowCard(
    modifier: Modifier = Modifier,
    glow: Color = NeonCyan,
    content: @Composable () -> Unit,
) {
    Card(
        modifier = modifier.drawBehind {
            // Soft outer glow behind the card.
            drawRoundRect(
                color = glow.copy(alpha = 0.10f),
                topLeft = Offset(-4f, 2f),
                size = size.copy(width = size.width + 8f, height = size.height + 6f),
                cornerRadius = androidx.compose.ui.geometry.CornerRadius(48f, 48f),
            )
        },
        shape = MaterialTheme.shapes.medium,
        colors = CardDefaults.cardColors(containerColor = BgPanel),
        border = BorderStroke(
            1.dp,
            Brush.linearGradient(
                listOf(glow.copy(alpha = 0.55f), NeonViolet.copy(alpha = 0.25f)),
            ),
        ),
        content = { content() },
    )
}

/** NeonButton — pill button with the neon primary gradient. */
@Composable
fun NeonButton(
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    enabled: Boolean = true,
    danger: Boolean = false,
    content: @Composable androidx.compose.foundation.layout.RowScope.() -> Unit,
) {
    val container = if (danger) NeonRed else NeonCyan
    Button(
        onClick = onClick,
        enabled = enabled,
        shape = RoundedCornerShape(50),
        colors = ButtonDefaults.buttonColors(
            containerColor = container,
            contentColor = OnNeon,
            disabledContainerColor = BgPanelHi,
            disabledContentColor = TextDim,
        ),
        contentPadding = PaddingValues(horizontal = 28.dp, vertical = 14.dp),
        modifier = modifier,
        content = content,
    )
}

/** Visual state of the [StatusRing]. */
enum class RingState { Idle, Connecting, Connected, Error }

/**
 * StatusRing — animated circular neon ring. Idle = dim grey, Connecting =
 * sweeping cyan arc, Connected = steady cyan/violet glow, Error = red.
 */
@Composable
fun StatusRing(
    state: RingState,
    modifier: Modifier = Modifier,
    diameter: Dp = 220.dp,
    content: @Composable () -> Unit,
) {
    val transition = rememberInfiniteTransition(label = "ring")
    val sweep by transition.animateFloat(
        initialValue = 0f,
        targetValue = 360f,
        animationSpec = infiniteRepeatable(
            animation = tween(1400, easing = LinearEasing),
            repeatMode = RepeatMode.Restart,
        ),
        label = "sweep",
    )
    val pulse by transition.animateFloat(
        initialValue = 0.35f,
        targetValue = 1f,
        animationSpec = infiniteRepeatable(
            animation = tween(1600, easing = FastOutSlowInEasing),
            repeatMode = RepeatMode.Reverse,
        ),
        label = "pulse",
    )
    val ringColor by animateColorAsState(
        targetValue = when (state) {
            RingState.Idle -> TextDim
            RingState.Connecting -> NeonCyan
            RingState.Connected -> NeonCyan
            RingState.Error -> NeonRed
        },
        label = "ringColor",
    )

    Box(
        modifier = modifier.size(diameter),
        contentAlignment = Alignment.Center,
    ) {
        Box(
            modifier = Modifier
                .fillMaxSize()
                .drawBehind {
                    val stroke = 10.dp.toPx()
                    val inset = stroke / 2 + 6f
                    val arcSize = androidx.compose.ui.geometry.Size(
                        size.width - inset * 2,
                        size.height - inset * 2,
                    )
                    val topLeft = Offset(inset, inset)
                    // Base track.
                    drawArc(
                        color = BgOutline,
                        startAngle = 0f,
                        sweepAngle = 360f,
                        useCenter = false,
                        topLeft = topLeft,
                        size = arcSize,
                        style = Stroke(width = stroke, cap = StrokeCap.Round),
                    )
                    when (state) {
                        RingState.Connecting -> {
                            drawArc(
                                brush = Brush.sweepGradient(
                                    listOf(Color.Transparent, NeonViolet, NeonCyan),
                                ),
                                startAngle = sweep,
                                sweepAngle = 110f,
                                useCenter = false,
                                topLeft = topLeft,
                                size = arcSize,
                                style = Stroke(width = stroke, cap = StrokeCap.Round),
                            )
                        }
                        RingState.Connected -> {
                            // Steady glowing ring with pulsing outer halo.
                            drawArc(
                                color = ringColor.copy(alpha = 0.18f * pulse),
                                startAngle = 0f,
                                sweepAngle = 360f,
                                useCenter = false,
                                topLeft = Offset(inset - 8f, inset - 8f),
                                size = androidx.compose.ui.geometry.Size(
                                    arcSize.width + 16f, arcSize.height + 16f,
                                ),
                                style = Stroke(width = stroke + 8f, cap = StrokeCap.Round),
                            )
                            drawArc(
                                brush = Brush.sweepGradient(listOf(NeonCyan, NeonViolet, NeonCyan)),
                                startAngle = 0f,
                                sweepAngle = 360f,
                                useCenter = false,
                                topLeft = topLeft,
                                size = arcSize,
                                style = Stroke(width = stroke, cap = StrokeCap.Round),
                            )
                        }
                        RingState.Error -> {
                            drawArc(
                                color = NeonRed,
                                startAngle = 0f,
                                sweepAngle = 360f,
                                useCenter = false,
                                topLeft = topLeft,
                                size = arcSize,
                                style = Stroke(width = stroke, cap = StrokeCap.Round),
                            )
                        }
                        RingState.Idle -> Unit
                    }
                },
        )
        content()
    }
}
