package app.kordo.android

import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import android.content.Intent
import android.provider.Settings
import android.widget.Toast
import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import app.kordo.core.mobile.Mobile
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import org.json.JSONObject

private fun copy(ctx: Context, text: String) {
    ctx.getSystemService(ClipboardManager::class.java).setPrimaryClip(ClipData.newPlainText("Kordo", text))
    Toast.makeText(ctx, "Скопировано. Выполните на компьютере с подключённым по USB телефоном.", Toast.LENGTH_LONG).show()
}

private fun open(ctx: Context, intent: Intent) {
    runCatching { ctx.startActivity(intent.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)) }
        .recoverCatching { ctx.startActivity(Intent(Settings.ACTION_SETTINGS).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)) }
}

/** Loads the report and wires the actions; the layout is [PrivacyContent]. */
@Composable
fun PrivacyScreen() {
    val ctx = LocalContext.current
    val scope = rememberCoroutineScope()
    val rev by revision
    val permRev by permissionsRevision
    var report by remember { mutableStateOf<PrivacyReport?>(null) }
    var reload by remember { mutableIntStateOf(0) }
    var busy by remember { mutableStateOf<String?>(null) }
    var settings by remember(rev) { mutableStateOf(runCatching { JSONObject(Mobile.privacySettings()) }.getOrDefault(JSONObject())) }
    val mock by MockLocation.state.collectAsStateWithLifecycle()

    LaunchedEffect(rev, permRev, reload) {
        report = withContext(Dispatchers.IO) { Privacy.report(ctx, Privacy.cachedServerPlace()) }
    }
    fun toast(s: String) = Toast.makeText(ctx, s, Toast.LENGTH_LONG).show()
    fun background(what: String, block: () -> Unit) {
        busy = what
        scope.launch {
            val r = withContext(Dispatchers.IO) { runCatching(block) }
            busy = null
            r.onFailure { toast(it.message ?: "$it") }
            reload++
        }
    }
    fun saveSettings(mut: JSONObject.() -> Unit) {
        val n = JSONObject(settings.toString()).apply(mut)
        runCatching { Mobile.setPrivacySettings(n.toString()) }.onSuccess { settings = n }.onFailure { toast(it.message ?: "$it") }
        if (VpnState.status.value == Status.CONNECTED) {
            MockLocation.stop()
            scope.launch(Dispatchers.IO) { MockLocation.start(ctx) }
        }
        reload++
    }

    PrivacyContent(
        report = report,
        busy = busy,
        mockEnabled = settings.optBoolean("mockLocation"),
        jitterM = settings.optInt("jitterM", 1500),
        mockState = mock,
        onLookup = { background("Определяю место сервера…") { Privacy.serverPlace(refresh = true) } },
        onManual = { lat, lon, cc -> background("Сохраняю…") { Mobile.setServerGeo("", lat, lon, cc, "") } },
        onClearManual = { background("Сбрасываю…") { Mobile.clearServerGeo(""); Privacy.serverPlace(refresh = true) } },
        onMock = { on -> saveSettings { put("mockLocation", on) } },
        onJitter = { m -> saveSettings { put("jitterM", m) } },
        onCheckRoot = { background("Проверяю root…") { if (!Privacy.checkRoot()) error("Root не найден или доступ не выдан") } },
        onFix = { fix ->
            when (fix) {
                is Fix.Open -> open(ctx, fix.intent)
                is Fix.Adb -> copy(ctx, fix.command)
                else -> background(fix.label) { Privacy.apply(ctx, fix) }
            }
        },
        onAppSettings = { open(ctx, Privacy.appSettingsIntent(it)) },
        onAppDeny = { pkg, deny ->
            background(if (deny) "Запрещаю…" else "Возвращаю…") {
                Privacy.su((if (deny) Privacy.denyCommands(pkg) else Privacy.allowCommands(pkg)).joinToString("; "))
            }
        },
        onCopyAdb = { pkg -> copy(ctx, Privacy.denyCommands(pkg).joinToString("\n") { "adb shell $it" }) },
        onCopy = { copy(ctx, it) },
    )
}

private val sevColor = mapOf(
    Severity.OK to Color(0xFF22C55E), Severity.INFO to Color(0xFF9AA3B5),
    Severity.WARN to Color(0xFFF59E0B), Severity.BAD to Color(0xFFEF4444),
)

@Composable
fun PrivacyContent(
    report: PrivacyReport?,
    busy: String?,
    mockEnabled: Boolean,
    jitterM: Int,
    mockState: MockLocation.State,
    onLookup: () -> Unit,
    onManual: (Double, Double, String) -> Unit,
    onClearManual: () -> Unit,
    onMock: (Boolean) -> Unit,
    onJitter: (Int) -> Unit,
    onCheckRoot: () -> Unit,
    onFix: (Fix) -> Unit,
    onAppSettings: (String) -> Unit,
    onAppDeny: (String, Boolean) -> Unit,
    onCopyAdb: (String) -> Unit,
    onCopy: (String) -> Unit,
) {
    var onlyVpn by remember { mutableStateOf(false) }
    var openApp by remember { mutableStateOf<AppExposure?>(null) }
    var manual by remember { mutableStateOf(false) }

    LazyColumn(Modifier.fillMaxSize(), contentPadding = PaddingValues(16.dp)) {
        item {
            Text("Приватность", fontSize = 26.sp, fontWeight = FontWeight.Bold)
            Text(
                "VPN скрывает IP, но приложения узнают регион и другими путями: GPS, сети Wi-Fi и Bluetooth вокруг, вышки, SIM, часовой пояс. Здесь видно, кто и через что может это сделать, и как закрыть.",
                color = MaterialTheme.colorScheme.onSurfaceVariant, fontSize = 14.sp,
            )
            busy?.let {
                Row(Modifier.padding(top = 8.dp), verticalAlignment = Alignment.CenterVertically) {
                    CircularProgressIndicator(Modifier.size(16.dp), strokeWidth = 2.dp)
                    Spacer(Modifier.width(8.dp)); Text(it, fontSize = 13.sp)
                }
            }
        }
        if (report == null) {
            item { Box(Modifier.fillMaxWidth().padding(32.dp), contentAlignment = Alignment.Center) { CircularProgressIndicator() } }
            return@LazyColumn
        }

        // level
        item {
            Section("Уровень возможностей")
            Card(colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surfaceVariant)) {
                Column(Modifier.padding(14.dp)) {
                    Row(Modifier.horizontalScroll(rememberScrollState())) {
                        Level.entries.forEach { l ->
                            FilterChip(selected = report.level == l, onClick = {}, label = { Text(l.title) }, modifier = Modifier.padding(end = 6.dp))
                        }
                    }
                    Text(report.level.about, fontSize = 13.sp, color = MaterialTheme.colorScheme.onSurfaceVariant)
                    if (report.level != Level.ROOT) {
                        Row(Modifier.padding(top = 6.dp)) {
                            OutlinedButton(onClick = onCheckRoot) { Text("Проверить root") }
                            if (report.level == Level.BASIC) {
                                Spacer(Modifier.width(8.dp))
                                TextButton(onClick = { onCopy("adb shell pm grant app.kordo.android android.permission.WRITE_SECURE_SETTINGS") }) { Text("Команда для уровня ADB") }
                            }
                        }
                    }
                }
            }
        }

        // server place + fake location
        item {
            Section("Место сервера и подмена GPS")
            Card(colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surfaceVariant)) {
                Column(Modifier.padding(14.dp)) {
                    val s = report.server
                    if (s == null) {
                        Text("Место сервера ещё не определено.", fontSize = 14.sp)
                    } else {
                        Text(listOf(s.city, s.country.ifEmpty { s.countryCode }).filter { it.isNotEmpty() }.joinToString(", ") +
                            if (s.manual) " (задано вручную)" else "", fontWeight = FontWeight.SemiBold)
                        Text("%.4f, %.4f · %s".format(s.lat, s.lon, s.timeZone.ifEmpty { "пояс неизвестен" }), fontSize = 13.sp,
                            color = MaterialTheme.colorScheme.onSurfaceVariant, fontFamily = FontFamily.Monospace)
                    }
                    Text("Определяется по адресу сервера через ipwho.is / ipapi.co: туда уходит только IP сервера.",
                        fontSize = 12.sp, color = MaterialTheme.colorScheme.onSurfaceVariant)
                    Row(Modifier.padding(top = 6.dp)) {
                        OutlinedButton(onClick = onLookup) { Text(if (s == null) "Определить" else "Обновить") }
                        Spacer(Modifier.width(8.dp))
                        TextButton(onClick = { manual = true }) { Text("Указать вручную") }
                        if (s?.manual == true) TextButton(onClick = onClearManual) { Text("Сбросить") }
                    }
                    HorizontalDivider(Modifier.padding(vertical = 10.dp))
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        Column(Modifier.weight(1f)) {
                            Text("Подменять местоположение", fontWeight = FontWeight.SemiBold)
                            Text("Пока VPN включён, все приложения видят место сервера вместо настоящего (GPS, сеть, Google). Касается всего телефона, включая карты и такси. Банковские и некоторые другие приложения умеют замечать подмену.",
                                fontSize = 12.sp, color = MaterialTheme.colorScheme.onSurfaceVariant)
                        }
                        Switch(mockEnabled, onMock)
                    }
                    if (mockEnabled) {
                        Text("Разброс от точки сервера", fontSize = 13.sp, modifier = Modifier.padding(top = 6.dp))
                        Row(Modifier.horizontalScroll(rememberScrollState())) {
                            listOf(0 to "нет", 500 to "500 м", 1500 to "1,5 км", 5000 to "5 км").forEach { (m, t) ->
                                FilterChip(selected = jitterM == m, onClick = { onJitter(m) }, label = { Text(t) }, modifier = Modifier.padding(end = 6.dp))
                            }
                        }
                    }
                    when {
                        mockState.active -> Text("Сейчас приложения видят: ${mockState.point}", color = sevColor[Severity.OK]!!, fontSize = 13.sp)
                        mockState.error != null -> Text(mockState.error, color = MaterialTheme.colorScheme.error, fontSize = 13.sp)
                        mockEnabled -> Text("Заработает после подключения VPN.", fontSize = 13.sp, color = MaterialTheme.colorScheme.onSurfaceVariant)
                    }
                }
            }
        }

        // system checks
        item { Section("Что выдаёт регион") }
        items(report.checks) { c -> CheckCard(c, onFix) }

        // apps
        item {
            Section("Приложения с доступом к месту")
            Text("Кто может узнать ваше настоящее место в обход VPN. Сверху — самые опасные: приложения, которые идут через VPN, но видят GPS/Wi-Fi/вышки.",
                fontSize = 13.sp, color = MaterialTheme.colorScheme.onSurfaceVariant)
            Row(Modifier.padding(vertical = 6.dp)) {
                FilterChip(selected = !onlyVpn, onClick = { onlyVpn = false }, label = { Text("Все") }, modifier = Modifier.padding(end = 6.dp))
                FilterChip(selected = onlyVpn, onClick = { onlyVpn = true }, label = { Text("Через VPN") })
            }
            Text("Любое приложение без разрешений видит страну SIM и сотовой сети, часовой пояс и язык системы — см. проверки выше.",
                fontSize = 12.sp, color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
        val shown = report.apps.filter { (!onlyVpn || it.viaVpn) && (it.channels.isNotEmpty() || it.blocked.isNotEmpty()) }
        if (shown.isEmpty()) item { Text("Таких приложений нет.", modifier = Modifier.padding(vertical = 8.dp)) }
        items(shown, key = { it.pkg }) { app -> AppRow(app) { openApp = app } }
    }

    openApp?.let { app ->
        AppDialog(app, report?.level ?: Level.BASIC, onClose = { openApp = null },
            onSettings = { onAppSettings(app.pkg) },
            onDeny = { deny -> onAppDeny(app.pkg, deny); openApp = null },
            onCopyAdb = { onCopyAdb(app.pkg) })
    }
    if (manual) ManualPlaceDialog(onClose = { manual = false }) { lat, lon, cc -> onManual(lat, lon, cc); manual = false }
}

@Composable
private fun Section(title: String) {
    Text(title, fontWeight = FontWeight.SemiBold, color = MaterialTheme.colorScheme.secondary, modifier = Modifier.padding(top = 18.dp, bottom = 6.dp))
}

@Composable
private fun CheckCard(c: Check, onFix: (Fix) -> Unit) {
    Card(Modifier.fillMaxWidth().padding(vertical = 4.dp), colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surfaceVariant)) {
        Row(Modifier.padding(14.dp)) {
            Surface(shape = MaterialTheme.shapes.extraLarge, color = sevColor[c.severity]!!, modifier = Modifier.padding(top = 6.dp).size(10.dp)) {}
            Spacer(Modifier.width(10.dp))
            Column(Modifier.weight(1f)) {
                Text(c.title, fontWeight = FontWeight.SemiBold)
                Text(c.detail, fontSize = 13.sp, color = MaterialTheme.colorScheme.onSurfaceVariant)
                if (c.fixes.isNotEmpty()) Row(Modifier.horizontalScroll(rememberScrollState()).padding(top = 4.dp)) {
                    c.fixes.forEach { f ->
                        val label = when (f) {
                            is Fix.Root -> f.label + " (root)"
                            is Fix.Adb -> f.label + " (копировать)"
                            else -> f.label
                        }
                        if (f is Fix.Open || f is Fix.Adb) OutlinedButton(onClick = { onFix(f) }, modifier = Modifier.padding(end = 6.dp)) { Text(label, fontSize = 13.sp) }
                        else Button(onClick = { onFix(f) }, modifier = Modifier.padding(end = 6.dp)) { Text(label, fontSize = 13.sp) }
                    }
                }
            }
        }
    }
}

@Composable
private fun AppRow(app: AppExposure, onClick: () -> Unit) {
    Column(Modifier.fillMaxWidth().clickable(onClick = onClick).padding(vertical = 8.dp)) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text(app.label, fontWeight = FontWeight.SemiBold, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f))
            if (app.viaVpn) Text("через VPN", fontSize = 12.sp, color = MaterialTheme.colorScheme.primary)
        }
        Text(app.pkg, fontSize = 12.sp, color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 1, overflow = TextOverflow.Ellipsis)
        Row(Modifier.horizontalScroll(rememberScrollState()).padding(top = 4.dp)) {
            app.channels.forEach { ch ->
                val danger = app.viaVpn && ch.weight >= 3
                AssistChip(onClick = onClick, label = { Text(ch.title, fontSize = 12.sp) }, modifier = Modifier.padding(end = 4.dp),
                    colors = if (danger) AssistChipDefaults.assistChipColors(labelColor = sevColor[Severity.BAD]!!) else AssistChipDefaults.assistChipColors())
            }
            app.blocked.forEach { ch ->
                AssistChip(onClick = onClick, label = { Text("${ch.title}: запрещено", fontSize = 12.sp) }, modifier = Modifier.padding(end = 4.dp),
                    colors = AssistChipDefaults.assistChipColors(labelColor = sevColor[Severity.OK]!!))
            }
        }
        HorizontalDivider(Modifier.padding(top = 6.dp))
    }
}

@Composable
private fun AppDialog(app: AppExposure, level: Level, onClose: () -> Unit, onSettings: () -> Unit, onDeny: (Boolean) -> Unit, onCopyAdb: () -> Unit) {
    AlertDialog(
        onDismissRequest = onClose,
        title = { Text(app.label) },
        text = {
            Column(Modifier.heightIn(max = 420.dp).verticalScroll(rememberScrollState())) {
                Text(if (app.viaVpn) "Идёт через VPN. Если у него есть доступ к месту ниже, он может узнать ваш настоящий регион несмотря на VPN."
                    else "Идёт напрямую — оно и так видит ваш настоящий IP.", fontSize = 13.sp)
                app.channels.forEach { ch ->
                    Text("• ${ch.title}", fontWeight = FontWeight.SemiBold, modifier = Modifier.padding(top = 8.dp))
                    Text(ch.about, fontSize = 13.sp, color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
                if (app.blocked.isNotEmpty()) Text("Уже запрещено: " + app.blocked.joinToString { it.title }, fontSize = 13.sp,
                    color = sevColor[Severity.OK]!!, modifier = Modifier.padding(top = 8.dp))
                Text(
                    when (level) {
                        Level.ROOT -> "С root Kordo может запретить приложению местоположение, поиск Wi-Fi и Bluetooth — приложение будет просто получать «нет данных», а сами разрешения останутся."
                        else -> "Без root: отзовите «Местоположение» и «Устройства поблизости» в настройках приложения. Или выполните команды ADB с компьютера — они запрещают доступ, не трогая разрешения."
                    },
                    fontSize = 12.sp, color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.padding(top = 10.dp),
                )
            }
        },
        confirmButton = {
            Column(horizontalAlignment = Alignment.End) {
                TextButton(onClick = onSettings) { Text("Настройки приложения") }
                if (level == Level.ROOT) {
                    if (app.channels.isNotEmpty()) TextButton(onClick = { onDeny(true) }) { Text("Запретить доступ (root)") }
                    if (app.blocked.isNotEmpty()) TextButton(onClick = { onDeny(false) }) { Text("Вернуть доступ (root)") }
                } else {
                    TextButton(onClick = onCopyAdb) { Text("Команды ADB") }
                }
            }
        },
        dismissButton = { TextButton(onClick = onClose) { Text("Закрыть") } },
    )
}

@Composable
private fun ManualPlaceDialog(onClose: () -> Unit, onSave: (Double, Double, String) -> Unit) {
    var lat by remember { mutableStateOf("") }
    var lon by remember { mutableStateOf("") }
    var cc by remember { mutableStateOf("") }
    val la = lat.replace(',', '.').toDoubleOrNull()
    val lo = lon.replace(',', '.').toDoubleOrNull()
    AlertDialog(
        onDismissRequest = onClose,
        title = { Text("Место сервера вручную") },
        text = {
            Column {
                Text("Например, из карт: долгота и широта точки, где «находится» сервер.", fontSize = 13.sp)
                OutlinedTextField(lat, { lat = it }, label = { Text("Широта, например 52.3676") }, singleLine = true,
                    keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Decimal), modifier = Modifier.fillMaxWidth())
                OutlinedTextField(lon, { lon = it }, label = { Text("Долгота, например 4.9041") }, singleLine = true,
                    keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Decimal), modifier = Modifier.fillMaxWidth())
                OutlinedTextField(cc, { cc = it.take(2).uppercase() }, label = { Text("Код страны, например NL") }, singleLine = true, modifier = Modifier.fillMaxWidth())
            }
        },
        confirmButton = { TextButton(enabled = la != null && lo != null, onClick = { onSave(la!!, lo!!, cc) }) { Text("Сохранить") } },
        dismissButton = { TextButton(onClick = onClose) { Text("Отмена") } },
    )
}
