package app.kordo.android

import android.content.ClipboardManager
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.ArrowBack
import androidx.compose.material.icons.filled.Delete
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.runtime.snapshots.SnapshotStateMap
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.input.VisualTransformation
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import app.kordo.core.mobile.Mobile
import org.json.JSONArray
import org.json.JSONObject

private val providerTitles = linkedMapOf("vless" to "VLESS", "shadowsocks" to "Shadowsocks", "wireguard" to "WireGuard")

/** Defaults for a new server of each protocol. */
private fun defaults(provider: String): Map<String, String> = when (provider) {
    "vless" -> mapOf("port" to "443", "security" to "reality", "fingerprint" to "chrome", "flow" to "xtls-rprx-vision", "transport" to "tcp")
    "shadowsocks" -> mapOf("port" to "8388", "method" to "2022-blake3-aes-128-gcm")
    "wireguard" -> mapOf("allowedIps" to "0.0.0.0/0, ::/0", "keepalive" to "25")
    else -> emptyMap()
}

/** params JSON -> flat string fields (arrays joined with ", "). */
private fun flatten(params: JSONObject): Map<String, String> = buildMap {
    params.keys().forEach { k ->
        when (val v = params.get(k)) {
            is JSONArray -> put(k, (0 until v.length()).joinToString(", ") { v.get(it).toString() })
            JSONObject.NULL -> {}
            else -> put(k, v.toString())
        }
    }
}

private fun list(v: String?): JSONArray = JSONArray((v ?: "").split(',', '\n').map { it.trim() }.filter { it.isNotEmpty() })

/** flat fields -> params JSON of the provider. */
private fun build(provider: String, f: Map<String, String>): JSONObject = JSONObject().apply {
    fun str(k: String) = f[k]?.trim().orEmpty()
    fun putStr(k: String) = str(k).takeIf { it.isNotEmpty() }?.let { put(k, it) }
    fun putInt(k: String) = str(k).toIntOrNull()?.let { put(k, it) }
    when (provider) {
        "vless" -> {
            putStr("server"); putInt("port"); putStr("uuid"); put("security", str("security").ifEmpty { "reality" })
            put("flow", str("flow")); put("fingerprint", str("fingerprint")); put("transport", str("transport").ifEmpty { "tcp" })
            putStr("sni"); putStr("publicKey"); putStr("shortId"); putStr("path"); putStr("host"); putStr("serviceName")
            if (str("insecure") == "true") put("insecure", true)
        }
        "shadowsocks" -> {
            putStr("server"); putInt("port"); putStr("method"); putStr("password")
        }
        "wireguard" -> {
            putStr("privateKey"); put("address", list(f["address"])); putInt("mtu")
            putStr("publicKey"); putStr("presharedKey"); putStr("endpoint"); put("allowedIps", list(f["allowedIps"])); putInt("keepalive")
        }
    }
}

/** Engine errors in plain Russian (the most common ones). */
fun ruError(msg: String?): String {
    val m = msg ?: return "Ошибка"
    return when {
        m.contains("privateKey", true) && m.contains("key", true) -> "PrivateKey: нужен ключ WireGuard (44 символа base64)"
        m.contains("presharedKey", true) -> "PresharedKey: нужен ключ base64 (44 символа) или пусто"
        m.contains("publicKey must", true) -> "PublicKey: нужен ключ WireGuard (44 символа base64)"
        m.contains("interface address", true) -> "Address: укажите адрес интерфейса, например 10.0.0.2/32"
        m.contains("invalid address", true) -> "Address: неверный адрес — нужен вид 10.0.0.2/32"
        m.contains("allowed IP", true) -> "AllowedIPs: неверная сеть — нужен вид 0.0.0.0/0"
        m.contains("endpoint", true) -> "Endpoint: нужен адрес вида host:51820"
        m.contains("uuid", true) -> "UUID: неверный формат"
        m.contains("port", true) -> "Порт: число от 1 до 65535"
        m.contains("server", true) && m.contains("required", true) -> "Укажите адрес сервера"
        m.contains("password", true) -> "Пароль: для шифров 2022 нужен ключ base64 нужной длины"
        m.contains("unsupported", true) || m.contains("unknown", true) -> "Не похоже на ссылку vless://, ss://, wireguard:// или конфиг WireGuard"
        else -> m
    }
}

/** "PrivateKey = abc" pasted into the PrivateKey field -> "abc". */
private fun stripKey(value: String, vararg keys: String): String {
    val t = value.trim()
    for (k in keys) {
        val r = Regex("^${Regex.escape(k)}\\s*=\\s*", RegexOption.IGNORE_CASE)
        if (r.containsMatchIn(t)) return t.replace(r, "")
    }
    return value
}

private fun looksLikeWholeConfig(v: String) =
    v.contains("[Interface]", true) || Regex("^(vless|ss|wireguard|wg)://", RegexOption.IGNORE_CASE).containsMatchIn(v.trim())

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ServerEditor(serverId: String?, initialTab: String? = null, onClose: () -> Unit) {
    val ctx = LocalContext.current
    val existing = remember(serverId) { serverId?.let { runCatching { JSONObject(Mobile.serverParams(it)) }.getOrNull() } }
    var tab by remember { mutableStateOf(existing?.optString("provider") ?: initialTab ?: "paste") }
    var name by remember { mutableStateOf(existing?.optString("name").orEmpty()) }
    val fields: SnapshotStateMap<String, String> = remember {
        mutableStateMapOf<String, String>().apply {
            if (existing != null) putAll(flatten(existing.getJSONObject("params"))) else putAll(defaults(initialTab ?: "vless"))
        }
    }
    var error by remember { mutableStateOf<String?>(null) }
    var confirmDelete by remember { mutableStateOf(false) }

    /** Fills the whole form from a link or a .conf; returns false if it is not one. */
    fun load(text: String): Boolean {
        val r = runCatching { JSONObject(Mobile.parseServer(text)) }
        r.onFailure { error = ruError(it.message) }
        val p = r.getOrNull() ?: return false
        fields.clear()
        fields.putAll(defaults(p.optString("provider")))
        fields.putAll(flatten(p.getJSONObject("params")))
        if (name.isBlank()) name = p.optString("name")
        tab = p.optString("provider")
        error = null
        return true
    }

    fun switchTo(provider: String) {
        if (tab != provider && tab != "paste") {
            fields.clear()
            fields.putAll(defaults(provider))
        } else if (tab == "paste") {
            fields.clear()
            fields.putAll(defaults(provider))
        }
        tab = provider
        error = null
    }

    Scaffold(topBar = {
        TopAppBar(
            title = { Text(if (serverId == null) "Новый сервер" else "Сервер") },
            navigationIcon = { IconButton(onClick = onClose) { Icon(Icons.Filled.ArrowBack, "Назад") } },
            actions = {
                if (serverId != null) IconButton(onClick = { confirmDelete = true }) { Icon(Icons.Filled.Delete, "Удалить") }
            },
        )
    }) { pad ->
        Column(Modifier.padding(pad).fillMaxSize().verticalScroll(rememberScrollState()).padding(horizontal = 16.dp)) {
            Row(Modifier.horizontalScroll(rememberScrollState()).padding(bottom = 8.dp)) {
                (listOf("paste" to "Вставить") + providerTitles.toList()).forEach { (k, v) ->
                    FilterChip(selected = tab == k, onClick = { if (k == "paste") tab = k else switchTo(k) }, label = { Text(v) }, modifier = Modifier.padding(end = 8.dp))
                }
            }
            if (tab == "paste") {
                PasteTab(onParsed = ::load)
            } else {
                Field("Название", name, { name = it }, hint = "Как сервер будет называться в списке")
                val onField: (String, String) -> Unit = { k, v ->
                    if (v.contains('\n') && looksLikeWholeConfig(v) || looksLikeWholeConfig(v) && v.length > 40) load(v) else fields[k] = v
                }
                when (tab) {
                    "vless" -> VlessForm(fields, onField)
                    "shadowsocks" -> ShadowsocksForm(fields, onField)
                    "wireguard" -> WireGuardForm(fields, onField)
                }
                error?.let { Text(it, color = MaterialTheme.colorScheme.error, modifier = Modifier.padding(vertical = 8.dp)) }
                Button(
                    onClick = {
                        runCatching { Mobile.saveServer(serverId ?: "", tab, name, build(tab, fields).toString()) }
                            .onSuccess { changed(); onClose() }
                            .onFailure { error = ruError(it.message) }
                    },
                    modifier = Modifier.fillMaxWidth().padding(vertical = 16.dp),
                ) { Text("Сохранить") }
            }
        }
    }
    if (confirmDelete && serverId != null) {
        AlertDialog(
            onDismissRequest = { confirmDelete = false },
            title = { Text("Удалить «$name»?") },
            text = { Text("Правила приложений для этого сервера тоже будут удалены.") },
            confirmButton = {
                TextButton(onClick = {
                    runCatching { Mobile.removeServer(serverId) }
                    changed()
                    onClose()
                }) { Text("Удалить") }
            },
            dismissButton = { TextButton(onClick = { confirmDelete = false }) { Text("Отмена") } },
        )
    }
    androidx.activity.compose.BackHandler(onBack = onClose)
    LaunchedEffect(Unit) {
        // a fresh "add" opens on Paste with the clipboard if it holds a link or config
        if (serverId == null) {
            val clip = ctx.getSystemService(ClipboardManager::class.java).primaryClip?.getItemAt(0)?.coerceToText(ctx)?.toString()
            if (clip != null && looksLikeWholeConfig(clip)) tab = "paste"
        }
    }
}

@Composable
private fun PasteTab(onParsed: (String) -> Boolean) {
    val ctx = LocalContext.current
    var text by remember { mutableStateOf("") }
    Text(
        "Вставьте ссылку vless://, ss://, wireguard:// или весь конфиг WireGuard (.conf). Kordo заполнит форму, её можно будет поправить перед сохранением.",
        color = MaterialTheme.colorScheme.onSurfaceVariant, fontSize = 14.sp,
    )
    Spacer(Modifier.height(8.dp))
    OutlinedTextField(
        text, { text = it }, modifier = Modifier.fillMaxWidth().heightIn(min = 160.dp),
        textStyle = LocalTextStyle.current.copy(fontFamily = FontFamily.Monospace, fontSize = 13.sp),
        placeholder = { Text("[Interface]\nPrivateKey = …\nAddress = …\n\n[Peer]\nPublicKey = …\nEndpoint = …") },
    )
    Row(Modifier.padding(vertical = 8.dp)) {
        OutlinedButton(onClick = {
            text = ctx.getSystemService(ClipboardManager::class.java).primaryClip?.getItemAt(0)?.coerceToText(ctx)?.toString() ?: text
        }) { Text("Из буфера") }
        Spacer(Modifier.width(8.dp))
        Button(onClick = { onParsed(text) }, enabled = text.isNotBlank()) { Text("Разобрать") }
    }
    Text("Нет ссылки под рукой? Выберите протокол вверху и заполните поля вручную.", color = MaterialTheme.colorScheme.onSurfaceVariant, fontSize = 13.sp)
}

@Composable
private fun Section(title: String) {
    Text(title, fontWeight = FontWeight.SemiBold, color = MaterialTheme.colorScheme.secondary, modifier = Modifier.padding(top = 16.dp, bottom = 4.dp))
}

@Composable
private fun Field(
    label: String, value: String, onChange: (String) -> Unit, hint: String? = null,
    number: Boolean = false, secret: Boolean = false, mono: Boolean = false,
) {
    var show by remember { mutableStateOf(false) }
    OutlinedTextField(
        value = value, onValueChange = onChange, label = { Text(label) },
        supportingText = hint?.let { { Text(it) } },
        singleLine = true,
        keyboardOptions = KeyboardOptions(keyboardType = if (number) KeyboardType.Number else KeyboardType.Ascii, autoCorrectEnabled = false),
        visualTransformation = if (secret && !show) PasswordVisualTransformation() else VisualTransformation.None,
        trailingIcon = if (secret) ({ TextButton(onClick = { show = !show }) { Text(if (show) "скрыть" else "показать", fontSize = 12.sp) } }) else null,
        textStyle = if (mono) LocalTextStyle.current.copy(fontFamily = FontFamily.Monospace) else LocalTextStyle.current,
        modifier = Modifier.fillMaxWidth().padding(vertical = 2.dp),
    )
}

@Composable
private fun Chips(label: String, value: String, options: List<Pair<String, String>>, onPick: (String) -> Unit) {
    Column(Modifier.padding(vertical = 4.dp)) {
        Text(label, fontSize = 13.sp, color = MaterialTheme.colorScheme.onSurfaceVariant)
        Row(Modifier.horizontalScroll(rememberScrollState())) {
            options.forEach { (v, t) ->
                FilterChip(selected = v == value, onClick = { onPick(v) }, label = { Text(t) }, modifier = Modifier.padding(end = 8.dp))
            }
        }
    }
}

@Composable
private fun Toggle(label: String, value: Boolean, onChange: (Boolean) -> Unit) {
    Row(Modifier.fillMaxWidth().padding(vertical = 4.dp), verticalAlignment = Alignment.CenterVertically) {
        Text(label, Modifier.weight(1f))
        Switch(value, onChange)
    }
}

@Composable
private fun VlessForm(f: SnapshotStateMap<String, String>, set: (String, String) -> Unit) {
    val security = f["security"] ?: "reality"
    val transport = f["transport"] ?: "tcp"
    Section("Подключение")
    Field("Сервер", f["server"].orEmpty(), { set("server", it.trim()) }, hint = "Домен или IP, например vpn.example.com")
    Field("Порт", f["port"].orEmpty(), { set("port", it.filter(Char::isDigit)) }, number = true)
    Field("UUID", f["uuid"].orEmpty(), { set("uuid", it.trim()) }, mono = true, hint = "Идентификатор пользователя на сервере")
    Section("Шифрование")
    Chips("Безопасность", security, listOf("reality" to "Reality", "tls" to "TLS", "none" to "Нет")) { set("security", it) }
    if (security != "none") {
        Field("SNI", f["sni"].orEmpty(), { set("sni", it.trim()) }, hint = if (security == "reality") "Сайт-маскировка, например www.microsoft.com" else "Домен сертификата")
        Chips("Отпечаток (uTLS)", f["fingerprint"].orEmpty(), listOf("chrome" to "Chrome", "firefox" to "Firefox", "safari" to "Safari", "ios" to "iOS", "android" to "Android", "random" to "Случайный", "" to "Нет")) { set("fingerprint", it) }
    }
    if (security == "reality") {
        Field("Public key (pbk)", f["publicKey"].orEmpty(), { set("publicKey", it.trim()) }, mono = true)
        Field("Short ID (sid)", f["shortId"].orEmpty(), { set("shortId", it.trim()) }, mono = true, hint = "Можно оставить пустым, если сервер не требует")
    }
    if (security == "tls") Toggle("Не проверять сертификат", f["insecure"] == "true") { set("insecure", it.toString()) }
    Toggle("XTLS Vision (flow)", f["flow"] == "xtls-rprx-vision") { set("flow", if (it) "xtls-rprx-vision" else "") }
    Section("Транспорт")
    Chips("Тип", transport, listOf("tcp" to "TCP", "ws" to "WebSocket", "grpc" to "gRPC", "httpupgrade" to "HTTPUpgrade", "http" to "HTTP")) { set("transport", it) }
    when (transport) {
        "ws", "httpupgrade", "http" -> {
            Field("Path", f["path"].orEmpty(), { set("path", it.trim()) }, hint = "Например /ws")
            Field("Host", f["host"].orEmpty(), { set("host", it.trim()) })
        }
        "grpc" -> Field("Service name", f["serviceName"].orEmpty(), { set("serviceName", it.trim()) })
    }
}

private val ssMethods = listOf(
    "2022-blake3-aes-128-gcm", "2022-blake3-aes-256-gcm", "2022-blake3-chacha20-poly1305",
    "aes-128-gcm", "aes-256-gcm", "chacha20-ietf-poly1305", "xchacha20-ietf-poly1305",
)

@Composable
private fun ShadowsocksForm(f: SnapshotStateMap<String, String>, set: (String, String) -> Unit) {
    Section("Подключение")
    Field("Сервер", f["server"].orEmpty(), { set("server", it.trim()) }, hint = "Домен или IP")
    Field("Порт", f["port"].orEmpty(), { set("port", it.filter(Char::isDigit)) }, number = true)
    Section("Шифрование")
    Chips("Шифр", f["method"].orEmpty(), ssMethods.map { it to it }) { set("method", it) }
    Field("Пароль / ключ", f["password"].orEmpty(), { set("password", it.trim()) }, secret = true, mono = true,
        hint = if ((f["method"] ?: "").startsWith("2022")) "Для шифров 2022 — ключ base64 с сервера" else null)
}

@Composable
private fun WireGuardForm(f: SnapshotStateMap<String, String>, set: (String, String) -> Unit) {
    Text(
        "Поля названы как строки в .conf. Можно вставлять строку целиком (например «PrivateKey = …») — лишнее уберётся само; а если вставить весь конфиг, форма заполнится полностью.",
        fontSize = 13.sp, color = MaterialTheme.colorScheme.onSurfaceVariant,
    )
    Section("[Interface]")
    Field("PrivateKey", f["privateKey"].orEmpty(), { set("privateKey", stripKey(it, "PrivateKey").trim()) }, secret = true, mono = true)
    Field("Address", f["address"].orEmpty(), { set("address", stripKey(it, "Address")) }, mono = true, hint = "Например 10.66.0.2/32; несколько — через запятую")
    Field("MTU", f["mtu"].orEmpty(), { set("mtu", stripKey(it, "MTU").filter(Char::isDigit)) }, number = true, hint = "Необязательно, по умолчанию 1408")
    Section("[Peer]")
    Field("PublicKey", f["publicKey"].orEmpty(), { set("publicKey", stripKey(it, "PublicKey").trim()) }, mono = true)
    Field("PresharedKey", f["presharedKey"].orEmpty(), { set("presharedKey", stripKey(it, "PresharedKey").trim()) }, secret = true, mono = true, hint = "Необязательно")
    Field("Endpoint", f["endpoint"].orEmpty(), { set("endpoint", stripKey(it, "Endpoint").trim()) }, mono = true, hint = "host:port, например vpn.example.com:51820")
    Field("AllowedIPs", f["allowedIps"].orEmpty(), { set("allowedIps", stripKey(it, "AllowedIPs", "AllowedIps")) }, mono = true, hint = "Обычно 0.0.0.0/0, ::/0")
    Field("PersistentKeepalive", f["keepalive"].orEmpty(), { set("keepalive", stripKey(it, "PersistentKeepalive").filter(Char::isDigit)) }, number = true, hint = "Секунды, обычно 25")
    Text("DNS из конфига не нужен: DNS для всех приложений задаётся в настройках Kordo.", fontSize = 12.sp, color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.padding(top = 4.dp))
}
