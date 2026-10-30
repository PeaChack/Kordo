package app.kordo.android

import android.Manifest
import android.content.ClipboardManager
import android.content.Context
import android.content.Intent
import android.content.pm.ApplicationInfo
import android.content.pm.PackageManager
import android.graphics.Bitmap
import android.graphics.Canvas
import android.net.Uri
import android.net.VpnService
import android.os.Build
import android.os.Bundle
import android.widget.Toast
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.Image
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import app.kordo.core.mobile.Mobile
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import org.json.JSONArray
import org.json.JSONObject

data class Server(val id: String, val name: String, val provider: String, val address: String)
data class AppItem(val pkg: String, val label: String, val system: Boolean, val info: ApplicationInfo)

class MainActivity : ComponentActivity() {
    private var connectAfterVpnGrant = false
    private val vpnPermission = registerForActivityResult(ActivityResultContracts.StartActivityForResult()) {
        permissionsRevision.intValue++
        if (VpnService.prepare(this) == null) {
            if (connectAfterVpnGrant) KordoVpnService.start(this)
        } else {
            Toast.makeText(this, "Без разрешения на VPN Kordo не может работать", Toast.LENGTH_LONG).show()
        }
        connectAfterVpnGrant = false
    }
    private val notificationPermission = registerForActivityResult(ActivityResultContracts.RequestPermission()) {
        permissionsRevision.intValue++
    }

    fun askVpn(thenConnect: Boolean = false) {
        val intent = VpnService.prepare(this)
        if (intent == null) {
            if (thenConnect) KordoVpnService.start(this)
            return
        }
        connectAfterVpnGrant = thenConnect
        runCatching { vpnPermission.launch(intent) }.onFailure {
            Toast.makeText(this, "Не удалось открыть запрос разрешения VPN", Toast.LENGTH_LONG).show()
        }
    }

    fun askNotifications() {
        if (Build.VERSION.SDK_INT < 33) return
        if (shouldShowRequestPermissionRationale(Manifest.permission.POST_NOTIFICATIONS) ||
            getSharedPreferences("kordo", MODE_PRIVATE).getBoolean("askedNotifications", false).not()
        ) {
            getSharedPreferences("kordo", MODE_PRIVATE).edit().putBoolean("askedNotifications", true).apply()
            notificationPermission.launch(Manifest.permission.POST_NOTIFICATIONS)
        } else {
            // denied twice: Android no longer shows the dialog, open the app's notification settings
            startActivity(Intent(android.provider.Settings.ACTION_APP_NOTIFICATION_SETTINGS)
                .putExtra(android.provider.Settings.EXTRA_APP_PACKAGE, packageName))
        }
    }

    fun connect() {
        if (Build.VERSION.SDK_INT >= 33 &&
            checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED &&
            !getSharedPreferences("kordo", MODE_PRIVATE).getBoolean("askedNotifications", false)
        ) askNotifications()
        askVpn(thenConnect = true)
    }

    override fun onResume() {
        super.onResume()
        permissionsRevision.intValue++ // the user may have changed something in system settings
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContent {
            KordoTheme {
                Surface(Modifier.fillMaxSize(), color = MaterialTheme.colorScheme.background) { KordoUi(this) }
            }
        }
    }
}

private enum class Tab(val title: String) { HOME("Подключение"), SERVERS("Серверы"), APPS("Приложения"), SETTINGS("Настройки") }

/** Bumped after every change so lists re-read the Go core. */
private val revision = mutableIntStateOf(0)
fun changed() {
    revision.intValue++
    VpnState.changed()
}

private fun servers(): List<Server> {
    val a = JSONArray(Mobile.servers())
    return (0 until a.length()).map { a.getJSONObject(it) }
        .map { Server(it.optString("id"), it.optString("name"), it.optString("provider"), it.optString("address")) }
}

private fun appRoutes(): Map<String, String> {
    val a = JSONArray(Mobile.apps())
    return (0 until a.length()).map { a.getJSONObject(it) }.associate { it.optString("package") to it.optString("target") }
}

private fun toast(ctx: Context, text: String) = Toast.makeText(ctx, text, Toast.LENGTH_LONG).show()

/** Full-screen pages shown over the tabs. */
private sealed interface Page {
    data object Permissions : Page
    data class Editor(val serverId: String?) : Page
}

private val page = mutableStateOf<Page?>(null)

@Composable
private fun KordoUi(activity: MainActivity) {
    var tab by remember { mutableStateOf(Tab.HOME) }
    val prefs = remember { activity.getSharedPreferences("kordo", Context.MODE_PRIVATE) }
    var onboarded by remember { mutableStateOf(prefs.getBoolean("onboarded", false)) }
    if (!onboarded) {
        PermissionsScreen(activity, onboarding = true) {
            prefs.edit().putBoolean("onboarded", true).apply()
            onboarded = true
        }
        return
    }
    when (val p = page.value) {
        Page.Permissions -> {
            androidx.activity.compose.BackHandler { page.value = null }
            PermissionsScreen(activity, onboarding = false) { page.value = null }
            return
        }
        is Page.Editor -> {
            ServerEditor(p.serverId) { page.value = null }
            return
        }
        null -> {}
    }
    UpdateCheck(activity)
    Scaffold(bottomBar = {
        NavigationBar {
            Tab.entries.forEach { t ->
                NavigationBarItem(
                    selected = tab == t, onClick = { tab = t }, label = { Text(t.title) },
                    icon = {
                        Icon(
                            when (t) {
                                Tab.HOME -> Icons.Filled.Home
                                Tab.SERVERS -> Icons.Filled.List
                                Tab.APPS -> Icons.Filled.Menu
                                Tab.SETTINGS -> Icons.Filled.Settings
                            }, null
                        )
                    },
                )
            }
        }
    }) { pad ->
        Box(Modifier.padding(pad).fillMaxSize()) {
            when (tab) {
                Tab.HOME -> HomeScreen(activity) { tab = Tab.SERVERS }
                Tab.SERVERS -> ServersScreen()
                Tab.APPS -> AppsScreen()
                Tab.SETTINGS -> SettingsScreen()
            }
        }
    }
}

// ------------------------------------------------------------------ home

@Composable
private fun HomeScreen(activity: MainActivity, toServers: () -> Unit) {
    val status by VpnState.status.collectAsStateWithLifecycle()
    val error by VpnState.error.collectAsStateWithLifecycle()
    val rev by revision
    val list = remember(rev) { servers() }
    val routed = remember(rev) { appRoutes().values.count { it != "direct" && it != "block" } }
    Column(Modifier.fillMaxSize().padding(24.dp), horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.Center) {
        val on = status == Status.CONNECTED
        val color = when (status) {
            Status.CONNECTED -> MaterialTheme.colorScheme.primary
            Status.OFF -> MaterialTheme.colorScheme.surfaceVariant
            else -> MaterialTheme.colorScheme.tertiary
        }
        Button(
            onClick = { if (status == Status.OFF) activity.connect() else KordoVpnService.stop(activity) },
            enabled = list.isNotEmpty() && status != Status.STOPPING,
            shape = CircleShape, modifier = Modifier.size(160.dp),
            colors = ButtonDefaults.buttonColors(containerColor = color),
        ) { Icon(PowerIcon, if (status == Status.OFF) "Подключить" else "Отключить", Modifier.size(72.dp)) }
        Spacer(Modifier.height(24.dp))
        Text(
            when (status) {
                Status.OFF -> "Отключено"
                Status.CONNECTING -> "Подключение…"
                Status.CONNECTED -> "Подключено"
                Status.STOPPING -> "Отключение…"
            }, fontSize = 24.sp, fontWeight = FontWeight.SemiBold
        )
        Text(
            if (on) "Приложений через VPN: $routed" else "Нажмите, чтобы подключиться",
            color = MaterialTheme.colorScheme.onSurfaceVariant
        )
        if (list.isEmpty()) {
            Spacer(Modifier.height(16.dp))
            OutlinedButton(onClick = toServers) { Text("Сначала добавьте сервер") }
        }
        val permRev by permissionsRevision
        val missing = remember(permRev) { !requiredGranted(activity) }
        if (missing) {
            Spacer(Modifier.height(16.dp))
            OutlinedButton(onClick = { page.value = Page.Permissions }) { Text("Нужны разрешения — выдать") }
        }
        error?.let {
            Spacer(Modifier.height(16.dp))
            Text(it, color = MaterialTheme.colorScheme.error)
        }
    }
}

// ------------------------------------------------------------------ servers

@Composable
private fun ServersScreen() {
    val rev by revision
    val list = remember(rev) { servers() }
    Box(Modifier.fillMaxSize()) {
        LazyColumn(Modifier.fillMaxSize(), contentPadding = PaddingValues(16.dp)) {
            item { Text("Серверы", fontSize = 26.sp, fontWeight = FontWeight.Bold); Spacer(Modifier.height(12.dp)) }
            if (list.isEmpty()) item {
                Text("Пока пусто. Нажмите «+»: можно вставить ссылку или конфиг, а можно заполнить поля вручную.",
                    color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
            items(list, key = { it.id }) { s ->
                Card(
                    onClick = { page.value = Page.Editor(s.id) },
                    modifier = Modifier.fillMaxWidth().padding(vertical = 4.dp),
                    colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surfaceVariant),
                ) {
                    Row(Modifier.padding(16.dp), verticalAlignment = Alignment.CenterVertically) {
                        Column(Modifier.weight(1f)) {
                            Text(s.name, fontWeight = FontWeight.SemiBold)
                            Text(s.address, color = MaterialTheme.colorScheme.onSurfaceVariant, fontSize = 13.sp)
                        }
                        AssistChip(onClick = { page.value = Page.Editor(s.id) }, label = { Text(providerLabel(s.provider)) })
                    }
                }
            }
        }
        ExtendedFloatingActionButton(
            onClick = { page.value = Page.Editor(null) },
            icon = { Icon(Icons.Filled.Add, null) }, text = { Text("Добавить") },
            modifier = Modifier.align(Alignment.BottomEnd).padding(16.dp),
        )
    }
}

private fun providerLabel(p: String) = when (p) {
    "vless" -> "VLESS"
    "shadowsocks" -> "Shadowsocks"
    "wireguard" -> "WireGuard"
    else -> p
}

// ------------------------------------------------------------------ apps

@Composable
private fun AppsScreen() {
    val ctx = androidx.compose.ui.platform.LocalContext.current
    val rev by revision
    val routes = remember(rev) { appRoutes() }
    val serverList = remember(rev) { servers() }
    var all by remember { mutableStateOf<List<AppItem>?>(null) }
    var query by remember { mutableStateOf("") }
    var showSystem by remember { mutableStateOf(false) }
    var editing by remember { mutableStateOf<AppItem?>(null) }
    LaunchedEffect(Unit) {
        all = withContext(Dispatchers.IO) {
            val pm = ctx.packageManager
            pm.getInstalledApplications(0)
                .filter { it.packageName != ctx.packageName }
                .map { AppItem(it.packageName, pm.getApplicationLabel(it).toString(), it.flags and ApplicationInfo.FLAG_SYSTEM != 0, it) }
                .sortedWith(compareBy({ it.pkg !in routes }, { it.label.lowercase() }))
        }
    }
    Column(Modifier.fillMaxSize().padding(horizontal = 16.dp)) {
        Text("Приложения", fontSize = 26.sp, fontWeight = FontWeight.Bold, modifier = Modifier.padding(top = 16.dp))
        Text(
            if (Mobile.settings().contains("\"mode\":\"all\"")) "Остальные приложения идут через VPN" else "Остальные приложения идут напрямую",
            color = MaterialTheme.colorScheme.onSurfaceVariant
        )
        OutlinedTextField(query, { query = it }, placeholder = { Text("Поиск") }, singleLine = true,
            leadingIcon = { Icon(Icons.Filled.Search, null) }, modifier = Modifier.fillMaxWidth().padding(vertical = 8.dp))
        Row(verticalAlignment = Alignment.CenterVertically) {
            Checkbox(showSystem, { showSystem = it })
            Text("Показывать системные")
        }
        val apps = all
        if (apps == null) {
            Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) { CircularProgressIndicator() }
        } else {
            val q = query.trim().lowercase()
            val shown = apps.filter { (showSystem || !it.system || it.pkg in routes) && (q.isEmpty() || it.label.lowercase().contains(q) || it.pkg.contains(q)) }
            LazyColumn(Modifier.fillMaxSize()) {
                items(shown, key = { it.pkg }) { app ->
                    val target = routes[app.pkg]
                    ListItem(
                        modifier = Modifier.clickable { editing = app },
                        leadingContent = { AppIcon(app) },
                        headlineContent = { Text(app.label, maxLines = 1, overflow = TextOverflow.Ellipsis) },
                        supportingContent = { Text(app.pkg, maxLines = 1, overflow = TextOverflow.Ellipsis) },
                        trailingContent = {
                            Text(targetLabel(target, serverList), color = if (target != null) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.onSurfaceVariant)
                        },
                    )
                }
            }
        }
    }
    editing?.let { app ->
        val options = buildList {
            add("" to "Как остальные")
            add("direct" to "Напрямую")
            serverList.forEach { add(it.id to "Через «${it.name}»") }
            add("block" to "Заблокировать")
        }
        AlertDialog(
            onDismissRequest = { editing = null },
            title = { Text(app.label) },
            text = {
                Column {
                    options.forEach { (value, label) ->
                        Row(Modifier.fillMaxWidth().clickable {
                            runCatching { Mobile.setAppRoute(app.pkg, value) }.onFailure { toast(ctx, it.message ?: "$it") }
                            editing = null
                            changed()
                        }.padding(vertical = 10.dp), verticalAlignment = Alignment.CenterVertically) {
                            RadioButton(selected = (routes[app.pkg] ?: "") == value, onClick = null)
                            Spacer(Modifier.width(8.dp))
                            Text(label)
                        }
                    }
                }
            },
            confirmButton = { TextButton(onClick = { editing = null }) { Text("Закрыть") } },
        )
    }
}

private fun targetLabel(target: String?, servers: List<Server>): String = when (target) {
    null -> ""
    "direct" -> "напрямую"
    "block" -> "блок"
    else -> servers.find { it.id == target }?.name ?: "?"
}

@Composable
private fun AppIcon(app: AppItem) {
    val ctx = androidx.compose.ui.platform.LocalContext.current
    val bmp by produceState<Bitmap?>(null, app.pkg) {
        value = withContext(Dispatchers.IO) {
            runCatching {
                val d = ctx.packageManager.getApplicationIcon(app.info)
                val b = Bitmap.createBitmap(96, 96, Bitmap.Config.ARGB_8888)
                d.setBounds(0, 0, 96, 96)
                d.draw(Canvas(b))
                b
            }.getOrNull()
        }
    }
    Box(Modifier.size(40.dp)) { bmp?.let { Image(it.asImageBitmap(), null, Modifier.fillMaxSize()) } }
}

// ------------------------------------------------------------------ settings

@Composable
private fun SettingsScreen() {
    val ctx = androidx.compose.ui.platform.LocalContext.current
    val rev by revision
    val s = remember(rev) { JSONObject(Mobile.settings()) }
    val serverList = remember(rev) { servers() }
    fun save(mut: JSONObject.() -> Unit) {
        val n = JSONObject(s.toString()).apply(mut)
        runCatching { Mobile.setSettings(n.toString()) }.onFailure { toast(ctx, it.message ?: "$it") }
        changed()
    }
    Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(16.dp)) {
        Text("Настройки", fontSize = 26.sp, fontWeight = FontWeight.Bold)
        Spacer(Modifier.height(8.dp))
        Choice("Через VPN идут", s.optString("mode"), listOf("only" to "Выбранные", "all" to "Все, кроме выбранных")) { v ->
            save {
                put("mode", v)
                if (v == "all" && optString("defaultServer").isEmpty()) put("defaultServer", serverList.firstOrNull()?.id ?: "")
            }
        }
        if (s.optString("mode") == "all" && serverList.size > 1) {
            Choice("Сервер для всех", s.optString("defaultServer"), serverList.map { it.id to it.name }) { v -> save { put("defaultServer", v) } }
        }
        Choice("DNS", s.optString("dns"), listOf("via_server" to "Через VPN", "doh_direct" to "DoH напрямую", "system" to "Системный")) { v -> save { put("dns", v) } }
        Toggle("Локальная сеть мимо VPN", s.optBoolean("allowLan")) { v -> save { put("allowLan", v) } }
        Choice("IPv6", s.optString("ipv6"), listOf("auto" to "Авто", "block" to "Блокировать")) { v -> save { put("ipv6", v) } }
        Text("Дополнительно", fontWeight = FontWeight.SemiBold, modifier = Modifier.padding(top = 16.dp))
        Choice("MTU туннеля", s.optInt("mtu").toString(), listOf("9000" to "9000", "1500" to "1500", "1280" to "1280")) { v -> save { put("mtu", v.toInt()) } }
        Choice("Стек TUN", s.optString("stack"), listOf("gvisor" to "gvisor", "mixed" to "mixed", "system" to "system")) { v -> save { put("stack", v) } }
        Text(
            "Kill switch на Android включается в системе: Настройки → Сеть → VPN → Kordo → «Постоянная VPN» и «Блокировать соединения без VPN».",
            color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.padding(top = 16.dp)
        )
        TextButton(onClick = { ctx.startActivity(Intent(android.provider.Settings.ACTION_VPN_SETTINGS)) }) { Text("Открыть настройки VPN") }
        OutlinedButton(onClick = { page.value = Page.Permissions }, modifier = Modifier.padding(top = 8.dp)) { Text("Разрешения") }
        HorizontalDivider(Modifier.padding(vertical = 12.dp))
        Text("Версия ${BuildConfig.VERSION_NAME}")
        UpdateButton()
        TextButton(onClick = { ctx.startActivity(Intent(Intent.ACTION_VIEW, Uri.parse("https://github.com/PeaChack/Kordo"))) }) { Text("Kordo на GitHub") }
    }
}

@Composable
private fun Choice(title: String, value: String, options: List<Pair<String, String>>, onPick: (String) -> Unit) {
    Column(Modifier.padding(vertical = 8.dp)) {
        Text(title, fontWeight = FontWeight.Medium)
        Row(Modifier.horizontalScroll(rememberScrollState())) {
            options.forEach { (v, label) ->
                FilterChip(selected = v == value, onClick = { onPick(v) }, label = { Text(label) }, modifier = Modifier.padding(end = 8.dp))
            }
        }
    }
}

@Composable
private fun Toggle(title: String, value: Boolean, onChange: (Boolean) -> Unit) {
    Row(Modifier.fillMaxWidth().padding(vertical = 8.dp), verticalAlignment = Alignment.CenterVertically) {
        Text(title, Modifier.weight(1f), fontWeight = FontWeight.Medium)
        Switch(value, onChange)
    }
}

// ------------------------------------------------------------------ updates

private val pendingUpdate = mutableStateOf<UpdateInfo?>(null)

/** Checks once per app start (at most once a day). */
@Composable
private fun UpdateCheck(activity: MainActivity) {
    LaunchedEffect(Unit) {
        val prefs = activity.getSharedPreferences("kordo", Context.MODE_PRIVATE)
        val now = System.currentTimeMillis()
        if (now - prefs.getLong("lastUpdateCheck", 0) < 24 * 3600 * 1000L) return@LaunchedEffect
        val info = withContext(Dispatchers.IO) { runCatching { Updater.check() }.getOrNull() } ?: return@LaunchedEffect
        prefs.edit().putLong("lastUpdateCheck", now).apply()
        if (info.available) pendingUpdate.value = info
    }
    pendingUpdate.value?.let { UpdateDialog(it) }
}

@Composable
private fun UpdateButton() {
    val ctx = androidx.compose.ui.platform.LocalContext.current
    val scope = rememberCoroutineScope()
    var busy by remember { mutableStateOf(false) }
    TextButton(enabled = !busy, onClick = {
        busy = true
        scope.launch {
            val r = withContext(Dispatchers.IO) { runCatching { Updater.check() } }
            busy = false
            r.onSuccess { info ->
                when {
                    info.available -> pendingUpdate.value = info
                    info.latest.isEmpty() -> toast(ctx, "Это сборка для разработки, обновления не проверяются")
                    else -> toast(ctx, "Установлена последняя версия (${info.current})")
                }
            }.onFailure { toast(ctx, it.message ?: "$it") }
        }
    }) { Text(if (busy) "Проверка…" else "Проверить обновления") }
}

@Composable
private fun UpdateDialog(info: UpdateInfo) {
    val ctx = androidx.compose.ui.platform.LocalContext.current
    val scope = rememberCoroutineScope()
    var busy by remember { mutableStateOf(false) }
    AlertDialog(
        onDismissRequest = { if (!busy) pendingUpdate.value = null },
        title = { Text("Доступна версия ${info.latest}") },
        text = { Text(if (busy) "Загрузка…" else "Установлена ${info.current}. Скачать и установить?") },
        confirmButton = {
            TextButton(enabled = !busy, onClick = {
                busy = true
                scope.launch {
                    val r = withContext(Dispatchers.IO) { runCatching { Updater.download(ctx, info) } }
                    busy = false
                    r.onSuccess { Updater.install(ctx, it); pendingUpdate.value = null }
                        .onFailure { toast(ctx, it.message ?: "$it") }
                }
            }) { Text("Обновить") }
        },
        dismissButton = {
            TextButton(enabled = !busy, onClick = {
                ctx.startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(info.page)))
            }) { Text("Что нового") }
        },
    )
}
