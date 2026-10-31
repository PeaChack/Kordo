package app.kordo.android

import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.darkColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.graphics.vector.PathParser
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.unit.dp

// Colours of the Windows app (style.css)
private val KordoBlue = Color(0xFF1F6BFF)
private val KordoCyan = Color(0xFF14C8E6)

private val Scheme = darkColorScheme(
    primary = KordoBlue,
    onPrimary = Color.White,
    secondary = KordoCyan,
    secondaryContainer = Color(0xFF1D3F8F), // selected chips
    onSecondaryContainer = Color.White,
    tertiary = Color(0xFFF59E0B),
    background = Color(0xFF0F121A),
    surface = Color(0xFF0F121A),
    surfaceVariant = Color(0xFF1C2230),
    surfaceContainer = Color(0xFF161B26),
    surfaceContainerHigh = Color(0xFF1C2230),
    onSurfaceVariant = Color(0xFF9AA3B5),
    error = Color(0xFFEF4444),
)

@Composable
fun KordoTheme(content: @Composable () -> Unit) = MaterialTheme(colorScheme = Scheme, content = content)

/** The power symbol as a vector (the ⏻ character is missing from Android fonts). */
val PowerIcon: ImageVector by lazy {
    ImageVector.Builder("power", 24.dp, 24.dp, 24f, 24f).apply {
        addPath(
            PathParser().parsePathString(
                "M13,3h-2v10h2L13,3zM17.83,5.17l-1.42,1.42C17.99,7.86 19,9.81 19,12c0,3.87 -3.13,7 -7,7s-7,-3.13 -7,-7" +
                    "c0,-2.19 1.01,-4.14 2.58,-5.42L6.17,5.17C4.23,6.82 3,9.26 3,12c0,4.97 4.03,9 9,9s9,-4.03 9,-9" +
                    "c0,-2.74 -1.23,-5.18 -3.17,-6.83z"
            ).toNodes(),
            fill = SolidColor(Color.White),
        )
    }.build()
}
