package app.kordo.android

import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.size
import androidx.compose.ui.unit.dp
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onRoot
import com.github.takahirom.roborazzi.captureRoboImage
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode

/** Renders screens that do not need the Go core into build/outputs/roborazzi/. */
@RunWith(RobolectricTestRunner::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "w411dp-h891dp-xxhdpi", application = android.app.Application::class)
class ScreensTest {
    @get:Rule
    val rule = createComposeRule()

    private fun shot(name: String, content: @Composable () -> Unit) {
        rule.setContent {
            KordoTheme { Surface(Modifier.fillMaxSize(), color = MaterialTheme.colorScheme.background) { content() } }
        }
        rule.onRoot().captureRoboImage("build/outputs/roborazzi/$name.png")
    }

    @Test fun permissions() = shot("permissions") {
        PermissionsContent(androidx.compose.ui.platform.LocalContext.current, onboarding = true, onAsk = {}, onDone = {})
    }
    @Test fun privacy() = shot("privacy") {
        val report = PrivacyReport(
            level = Level.BASIC,
            server = ServerPlace("NL", "Netherlands", "Amsterdam", "Europe/Amsterdam", 52.37, 4.89, false),
            checks = listOf(
                Check("Фоновый поиск Wi-Fi", Severity.WARN, "Включён: телефон ищет сети Wi-Fi вокруг.",
                    listOf(Fix.Adb("Команда ADB", "adb shell settings put global wifi_scan_always_enabled 0"))),
                Check("Часовой пояс", Severity.WARN, "Телефон: Europe/Moscow. Сервер: Europe/Amsterdam."),
                Check("Страна SIM и сотовой сети", Severity.BAD, "SIM: Россия. Сеть: Россия."),
            ),
            apps = listOf(
                AppExposure("org.telegram.messenger", "Telegram", false, "a1", true, listOf(Channel.FINE, Channel.WIFI, Channel.BLUETOOTH, Channel.CELL), emptyList()),
                AppExposure("com.yandex.maps", "Карты", false, null, false, listOf(Channel.FINE, Channel.BACKGROUND), emptyList()),
            ),
        )
        PrivacyContent(report, null, mockEnabled = true, jitterM = 1500, mockState = MockLocation.State(error = "Kordo не выбран в «Для разработчиков»"),
            onLookup = {}, onManual = { _, _, _ -> }, onClearManual = {}, onMock = {}, onJitter = {}, onCheckRoot = {},
            onFix = {}, onAppSettings = {}, onAppDeny = { _, _ -> }, onCopyAdb = {}, onCopy = {})
    }
    @Test fun editorPaste() = shot("editor_paste") { ServerEditor(null) {} }
    @Test fun editorVless() = shot("editor_vless") { ServerEditor(null, "vless") {} }
    @Test fun editorShadowsocks() = shot("editor_shadowsocks") { ServerEditor(null, "shadowsocks") {} }
    @Test fun editorWireGuard() = shot("editor_wireguard") { ServerEditor(null, "wireguard") {} }
    @Test fun powerButton() = shot("power_button") {
        androidx.compose.foundation.layout.Box(contentAlignment = androidx.compose.ui.Alignment.Center) {
            androidx.compose.material3.Button(
                onClick = {}, shape = androidx.compose.foundation.shape.CircleShape,
                modifier = Modifier.size(160.dp),
            ) { androidx.compose.material3.Icon(PowerIcon, null, Modifier.size(72.dp)) }
        }
    }
}
