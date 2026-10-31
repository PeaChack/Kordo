package app.kordo.android

import android.Manifest
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.net.VpnService
import android.os.Build
import android.os.PowerManager
import android.provider.Settings
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.CheckCircle
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp

/** One thing Kordo needs from the user, with how to check and how to ask. */
data class Need(
    val title: String,
    val why: String,
    val required: Boolean,
    val granted: (Context) -> Boolean,
    val ask: (MainActivity) -> Unit,
)

val needs: List<Need> = buildList {
    add(Need(
        "VPN", "Без этого разрешения Android не даст Kordo направлять трафик приложений через сервер.", true,
        granted = { VpnService.prepare(it) == null },
        ask = { it.askVpn() },
    ))
    if (Build.VERSION.SDK_INT >= 33) add(Need(
        "Уведомления", "Android показывает значок работающего VPN и кнопку «Отключить» только с этим разрешением.", true,
        granted = { it.checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) == PackageManager.PERMISSION_GRANTED },
        ask = { it.askNotifications() },
    ))
    add(Need(
        "Работа в фоне", "Иначе система может «усыпить» Kordo, и VPN отключится, пока телефон лежит. Найдите Kordo в списке и выберите «Не ограничивать».", false,
        granted = { it.getSystemService(PowerManager::class.java).isIgnoringBatteryOptimizations(it.packageName) },
        ask = { it.startActivity(Intent(Settings.ACTION_IGNORE_BATTERY_OPTIMIZATION_SETTINGS)) },
    ))
    add(Need(
        "Установка обновлений", "Чтобы Kordo мог сам поставить новую версию. Можно разрешить позже, когда выйдет обновление.", false,
        granted = { it.packageManager.canRequestPackageInstalls() },
        ask = { it.startActivity(Intent(Settings.ACTION_MANAGE_UNKNOWN_APP_SOURCES, Uri.parse("package:${it.packageName}"))) },
    ))
}

fun requiredGranted(ctx: Context) = needs.filter { it.required }.all { it.granted(ctx) }

/** Bumped in onResume so the screen re-reads what was granted in system settings. */
val permissionsRevision = mutableIntStateOf(0)

@Composable
fun PermissionsScreen(activity: MainActivity, onboarding: Boolean, onDone: () -> Unit) =
    PermissionsContent(activity, onboarding, onAsk = { it.ask(activity) }, onDone = onDone)

@Composable
fun PermissionsContent(ctx: Context, onboarding: Boolean, onAsk: (Need) -> Unit, onDone: () -> Unit) {
    val rev by permissionsRevision
    Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(20.dp)) {
        Text(if (onboarding) "Добро пожаловать в Kordo" else "Разрешения", fontSize = 26.sp, fontWeight = FontWeight.Bold)
        Spacer(Modifier.height(6.dp))
        Text(
            "Kordo пускает через ваш VPN-сервер только выбранные приложения. Для этого ему нужно несколько разрешений.",
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        Spacer(Modifier.height(16.dp))
        needs.forEach { need ->
            val granted = remember(rev) { need.granted(ctx) }
            Card(Modifier.fillMaxWidth().padding(vertical = 6.dp), colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surfaceVariant)) {
                Row(Modifier.padding(16.dp), verticalAlignment = Alignment.CenterVertically) {
                    Column(Modifier.weight(1f)) {
                        Text(need.title, fontWeight = FontWeight.SemiBold, color = MaterialTheme.colorScheme.onSurface)
                        if (!need.required) Text("необязательно", fontSize = 12.sp, color = MaterialTheme.colorScheme.secondary)
                        Text(need.why, fontSize = 13.sp, color = MaterialTheme.colorScheme.onSurfaceVariant)
                    }
                    Spacer(Modifier.width(12.dp))
                    if (granted) {
                        Icon(Icons.Filled.CheckCircle, "Разрешено", tint = MaterialTheme.colorScheme.secondary)
                    } else {
                        FilledTonalButton(onClick = { onAsk(need) }) { Text("Разрешить") }
                    }
                }
            }
        }
        Spacer(Modifier.height(16.dp))
        val ready = remember(rev) { requiredGranted(ctx) }
        Button(onClick = onDone, enabled = ready || !onboarding, modifier = Modifier.fillMaxWidth()) {
            Text(if (onboarding) "Продолжить" else "Готово")
        }
        if (onboarding && !ready) {
            TextButton(onClick = onDone, modifier = Modifier.align(Alignment.CenterHorizontally)) { Text("Пропустить, разрешу позже") }
        }
    }
}
