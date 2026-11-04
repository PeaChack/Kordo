package app.kordo.android

import android.Manifest
import android.app.AppOpsManager
import android.content.Context
import android.content.Intent
import android.content.pm.ApplicationInfo
import android.content.pm.PackageInfo
import android.content.pm.PackageManager
import android.location.LocationManager
import android.net.Uri
import android.os.Build
import android.provider.Settings
import android.telephony.TelephonyManager
import app.kordo.core.mobile.Mobile
import org.json.JSONArray
import org.json.JSONObject
import java.util.Locale
import java.util.TimeZone
import java.util.concurrent.TimeUnit

/** How much Kordo is allowed to change on this phone. */
enum class Level(val title: String, val about: String) {
    BASIC("Обычный", "Kordo показывает, что выдаёт регион, и открывает нужные настройки."),
    ADB("ADB", "Kordo сам выключает фоновый поиск Wi-Fi и Bluetooth (разрешение выдано через ADB)."),
    ROOT("Root", "Kordo сам запрещает приложениям доступ к местоположению, Wi-Fi и Bluetooth и меняет часовой пояс."),
}

/** A way to fix a finding, from the most to the least capable. */
sealed interface Fix {
    val label: String
    data class Open(override val label: String, val intent: Intent) : Fix
    data class SecureSetting(override val label: String, val key: String, val value: Int) : Fix
    data class Root(override val label: String, val command: String) : Fix
    data class Adb(override val label: String, val command: String) : Fix
}

enum class Severity { OK, INFO, WARN, BAD }

data class Check(val title: String, val severity: Severity, val detail: String, val fixes: List<Fix> = emptyList())

/** One way an app can learn where the phone is. */
enum class Channel(val title: String, val about: String, val weight: Int) {
    FINE("Точное место (GPS)", "Координаты с точностью до метров: GPS, Wi-Fi и вышки вместе.", 5),
    COARSE("Примерное место", "Район или город — по сетям вокруг, без GPS.", 3),
    BACKGROUND("Место в фоне", "Может узнавать местоположение, даже когда закрыто.", 2),
    WIFI("Сети Wi-Fi рядом", "Список точек доступа вокруг. По базам Google/Apple он даёт место с точностью до дома.", 4),
    BLUETOOTH("Bluetooth рядом", "Маячки и устройства вокруг — помогают определить место, особенно в помещениях.", 2),
    CELL("Сотовые вышки", "Идентификаторы вышек дают город и район (доступны вместе с точным местом).", 3),
    PHONE("Данные телефона", "Оператор, тип сети, состояние SIM.", 1),
}

data class AppExposure(
    val pkg: String,
    val label: String,
    val system: Boolean,
    val route: String?,         // null = как остальные
    val viaVpn: Boolean,
    val channels: List<Channel>,
    val blocked: List<Channel>, // denied by appops (root) even though the permission is granted
) {
    val risk: Int get() = channels.sumOf { it.weight } * (if (viaVpn) 2 else 1)
}

data class ServerPlace(val countryCode: String, val country: String, val city: String, val timeZone: String, val lat: Double, val lon: Double, val manual: Boolean)

data class PrivacyReport(
    val level: Level,
    val server: ServerPlace?,
    val checks: List<Check>,
    val apps: List<AppExposure>,
)

object Privacy {
    private const val KORDO = "app.kordo.android"
    private const val WIFI_SCAN = "wifi_scan_always_enabled"
    private const val BLE_SCAN = "ble_scan_always_enabled"

    // ---------------------------------------------------------------- levels

    @Volatile private var rootChecked: Boolean? = null

    fun hasSecureSettings(ctx: Context) =
        ctx.checkSelfPermission(Manifest.permission.WRITE_SECURE_SETTINGS) == PackageManager.PERMISSION_GRANTED

    /** Asks su once (the root manager shows its prompt). Blocking. */
    fun checkRoot(): Boolean {
        rootChecked?.let { return it }
        val ok = runCatching { su("id").contains("uid=0") }.getOrDefault(false)
        rootChecked = ok
        return ok
    }

    fun rootKnown(): Boolean = rootChecked == true

    fun level(ctx: Context) = when {
        rootChecked == true -> Level.ROOT
        hasSecureSettings(ctx) -> Level.ADB
        else -> Level.BASIC
    }

    /** Runs a shell command as root and returns its output. Blocking. */
    fun su(command: String): String {
        val p = ProcessBuilder("su", "-c", command).redirectErrorStream(true).start()
        val out = p.inputStream.bufferedReader().readText()
        if (!p.waitFor(20, TimeUnit.SECONDS)) {
            p.destroy()
            error("su: timeout")
        }
        if (p.exitValue() != 0) error("su: ${out.trim().ifEmpty { "exit ${p.exitValue()}" }}")
        return out
    }

    /** Applies a fix that needs no activity (secure setting or root). Blocking. */
    fun apply(ctx: Context, fix: Fix) {
        when (fix) {
            is Fix.SecureSetting -> check(Settings.Global.putInt(ctx.contentResolver, fix.key, fix.value)) { "Android не дал изменить настройку" }
            is Fix.Root -> su(fix.command)
            else -> error("needs an activity")
        }
    }

    // ---------------------------------------------------------------- server

    private fun place(js: String): ServerPlace {
        val j = JSONObject(js)
        return ServerPlace(
            j.optString("countryCode"), j.optString("country"), j.optString("city"), j.optString("timeZone"),
            j.optDouble("lat"), j.optDouble("lon"), j.optBoolean("manual"),
        )
    }

    /** Looks the server up (network) unless cached; refresh forces a lookup. Blocking, throws. */
    fun serverPlace(refresh: Boolean): ServerPlace = place(Mobile.serverGeo("", refresh))

    /** Cached place only, no network. */
    fun cachedServerPlace(): ServerPlace? =
        runCatching { Mobile.cachedServerGeo("").takeIf { it.isNotEmpty() }?.let(::place) }.getOrNull()

    // ---------------------------------------------------------------- audit

    private fun countryName(code: String): String =
        if (code.isBlank()) "" else Locale("", code).getDisplayCountry(Locale("ru")).ifBlank { code }

    /** Builds the report. Blocking (reads every installed package). */
    fun report(ctx: Context, server: ServerPlace?): PrivacyReport {
        val level = level(ctx)
        return PrivacyReport(level, server, systemChecks(ctx, server, level), appsExposure(ctx, level))
    }

    private fun adbFor(command: String) = "adb shell $command"

    fun systemChecks(ctx: Context, server: ServerPlace?, level: Level): List<Check> = buildList {
        val cr = ctx.contentResolver
        val serverCc = server?.countryCode?.uppercase().orEmpty()
        val serverName = server?.let { countryName(it.countryCode) }.orEmpty()

        // location master switch
        val lm = ctx.getSystemService(LocationManager::class.java)
        val locOn = if (Build.VERSION.SDK_INT >= 28) lm.isLocationEnabled else true
        add(Check(
            "Местоположение на телефоне",
            if (locOn) Severity.INFO else Severity.OK,
            if (locOn) "Включено: приложения с разрешением видят ваше реальное место (если не включена подмена ниже)."
            else "Выключено: приложения не получают координаты, Wi-Fi и вышки для определения места.",
            listOf(Fix.Open("Настройки местоположения", Intent(Settings.ACTION_LOCATION_SOURCE_SETTINGS))),
        ))

        // background scanning
        for ((key, title, what) in listOf(
            Triple(WIFI_SCAN, "Фоновый поиск Wi-Fi", "сети Wi-Fi вокруг"),
            Triple(BLE_SCAN, "Фоновый поиск Bluetooth", "Bluetooth-устройства и маячки вокруг"),
        )) {
            val on = Settings.Global.getInt(cr, key, 0) == 1
            val fixes = buildList {
                if (on) {
                    if (level == Level.ADB || level == Level.ROOT) add(Fix.SecureSetting("Выключить", key, 0))
                    else add(Fix.Adb("Команда ADB", adbFor("settings put global $key 0")))
                    add(Fix.Open("Открыть настройки", scanningIntent()))
                }
            }
            add(Check(
                title, if (on) Severity.WARN else Severity.OK,
                if (on) "Включён: телефон ищет $what, даже когда Wi-Fi/Bluetooth выключены, и отдаёт их сервисам определения места."
                else "Выключен.",
                fixes,
            ))
        }

        // mock location
        val mockAllowed = MockLocation.allowed(ctx)
        val mockOn = MockLocation.enabledInSettings()
        add(Check(
            "Подмена местоположения",
            when {
                mockOn && mockAllowed -> Severity.OK
                mockOn -> Severity.BAD
                else -> Severity.INFO
            },
            when {
                mockOn && mockAllowed -> "Пока VPN включён, все приложения видят место сервера${if (serverName.isNotEmpty()) " ($serverName)" else ""}."
                mockOn -> "Включена в Kordo, но Kordo не выбран как приложение для фиктивных местоположений — подмена не работает."
                mockAllowed -> "Kordo может подменять место — включите подмену ниже."
                else -> "Выключена. Чтобы включить, Kordo нужно выбрать в «Для разработчиках» → «Приложение для фиктивных местоположений»."
            },
            buildList {
                if (!mockAllowed) {
                    add(Fix.Open("Для разработчиков", Intent(Settings.ACTION_APPLICATION_DEVELOPMENT_SETTINGS)))
                    if (level == Level.ROOT) add(Fix.Root("Выбрать Kordo (root)", "appops set $KORDO android:mock_location allow"))
                    else add(Fix.Adb("Команда ADB", adbFor("appops set $KORDO android:mock_location allow")))
                }
            },
        ))

        // time zone
        val tz = TimeZone.getDefault().id
        val tzOk = server == null || server.timeZone.isBlank() || sameOffset(tz, server.timeZone)
        add(Check(
            "Часовой пояс",
            if (tzOk) Severity.OK else Severity.WARN,
            buildString {
                append("Телефон: $tz.")
                if (server != null && server.timeZone.isNotBlank()) append(" Сервер: ${server.timeZone}.")
                if (!tzOk) append(" Любое приложение видит пояс без разрешений — расхождение с IP выдаёт настоящий регион.")
            },
            server?.takeUnless { tzOk }?.let { s ->
                buildList {
                    if (level == Level.ROOT) add(Fix.Root("Поставить пояс сервера", setTimeZoneCommand(s.timeZone)))
                    add(Fix.Open("Дата и время", Intent(Settings.ACTION_DATE_SETTINGS)))
                }
            } ?: emptyList(),
        ))

        // SIM / network country
        val tm = ctx.getSystemService(TelephonyManager::class.java)
        val sim = tm?.simCountryIso.orEmpty().uppercase()
        val net = tm?.networkCountryIso.orEmpty().uppercase()
        if (sim.isNotEmpty() || net.isNotEmpty()) {
            val mismatch = serverCc.isNotEmpty() && (sim.isNotEmpty() && sim != serverCc || net.isNotEmpty() && net != serverCc)
            add(Check(
                "Страна SIM и сотовой сети",
                if (mismatch) Severity.BAD else Severity.OK,
                buildString {
                    if (sim.isNotEmpty()) append("SIM: ${countryName(sim)}. ")
                    if (net.isNotEmpty()) append("Сеть: ${countryName(net)}. ")
                    append("Любое приложение узнаёт это без разрешений; подменить без root-модуля нельзя.")
                    if (mismatch) append(" Не совпадает со страной сервера.")
                },
                if (mismatch) listOf(Fix.Open("Режим полёта / только Wi-Fi", Intent(Settings.ACTION_AIRPLANE_MODE_SETTINGS))) else emptyList(),
            ))
        }

        // language / region
        val loc = Locale.getDefault()
        val regionMismatch = serverCc.isNotEmpty() && loc.country.isNotEmpty() && !loc.country.equals(serverCc, true)
        add(Check(
            "Язык и регион системы",
            if (regionMismatch) Severity.INFO else Severity.OK,
            "Сейчас: ${loc.displayName}. Приложения видят это без разрешений. Обычно язык не выдаёт местоположение сам по себе, но вместе с поясом и SIM — дополняет картину.",
            if (regionMismatch) listOf(Fix.Open("Языки и регион", Intent(Settings.ACTION_LOCALE_SETTINGS))) else emptyList(),
        ))
    }

    private fun sameOffset(a: String, b: String): Boolean {
        val now = System.currentTimeMillis()
        return TimeZone.getTimeZone(a).getOffset(now) == TimeZone.getTimeZone(b).getOffset(now)
    }

    fun setTimeZoneCommand(tz: String) =
        "settings put global auto_time_zone 0; cmd alarm set-timezone $tz 2>/dev/null || service call alarm 3 s16 $tz"

    private fun scanningIntent() =
        Intent("android.settings.LOCATION_SCANNING_SETTINGS")

    /** appops commands that cut an app off from every location channel. */
    fun denyCommands(pkg: String): List<String> = buildList {
        add("appops set $pkg COARSE_LOCATION ignore")
        add("appops set $pkg FINE_LOCATION ignore")
        add("appops set $pkg MONITOR_LOCATION ignore")
        add("appops set $pkg WIFI_SCAN ignore")
        if (Build.VERSION.SDK_INT >= 31) add("appops set $pkg BLUETOOTH_SCAN ignore")
    }

    fun allowCommands(pkg: String): List<String> = denyCommands(pkg).map { it.replace(" ignore", " default") }

    // ---------------------------------------------------------------- apps

    @Suppress("DEPRECATION")
    fun appsExposure(ctx: Context, level: Level): List<AppExposure> {
        val pm = ctx.packageManager
        val ops = ctx.getSystemService(AppOpsManager::class.java)
        val routes = runCatching {
            val a = JSONArray(Mobile.apps())
            (0 until a.length()).associate { a.getJSONObject(it).let { o -> o.optString("package") to o.optString("target") } }
        }.getOrDefault(emptyMap())
        val allVia = runCatching { JSONObject(Mobile.settings()).optString("mode") == "all" }.getOrDefault(false)
        val pkgs = if (Build.VERSION.SDK_INT >= 33) {
            pm.getInstalledPackages(PackageManager.PackageInfoFlags.of(PackageManager.GET_PERMISSIONS.toLong()))
        } else {
            pm.getInstalledPackages(PackageManager.GET_PERMISSIONS)
        }
        return pkgs.mapNotNull { info -> exposure(pm, ops, info, routes, allVia) }
            .filter { it.channels.isNotEmpty() || it.route != null }
            .sortedWith(compareByDescending<AppExposure> { it.risk }.thenBy { it.label.lowercase() })
    }

    private fun exposure(pm: PackageManager, ops: AppOpsManager, info: PackageInfo, routes: Map<String, String>, allVia: Boolean): AppExposure? {
        val app = info.applicationInfo ?: return null
        if (info.packageName == KORDO) return null
        val req = info.requestedPermissions ?: return null
        val flags = info.requestedPermissionsFlags ?: IntArray(req.size)
        val granted = req.indices.filter { flags[it] and PackageInfo.REQUESTED_PERMISSION_GRANTED != 0 }.map { req[it] }.toSet()
        fun has(p: String) = p in granted
        fun opAllowed(op: String) = runCatching {
            val m = checkOp(ops, op, app.uid, info.packageName)
            m == AppOpsManager.MODE_ALLOWED || m == AppOpsManager.MODE_FOREGROUND
        }.getOrDefault(true)

        val channels = mutableListOf<Channel>()
        val blocked = mutableListOf<Channel>()
        fun add(c: Channel, permitted: Boolean, op: String?) {
            if (!permitted) return
            if (op != null && !opAllowed(op)) blocked += c else channels += c
        }
        val fine = has(Manifest.permission.ACCESS_FINE_LOCATION)
        val coarse = has(Manifest.permission.ACCESS_COARSE_LOCATION)
        add(Channel.FINE, fine, AppOpsManager.OPSTR_FINE_LOCATION)
        add(Channel.COARSE, coarse && !fine, AppOpsManager.OPSTR_COARSE_LOCATION)
        add(Channel.BACKGROUND, (fine || coarse) && has("android.permission.ACCESS_BACKGROUND_LOCATION"), null)
        val nearbyWifi = Build.VERSION.SDK_INT >= 33 && has("android.permission.NEARBY_WIFI_DEVICES")
        add(Channel.WIFI, nearbyWifi || ((fine || coarse) && has(Manifest.permission.ACCESS_WIFI_STATE)), "android:wifi_scan")
        val bt = if (Build.VERSION.SDK_INT >= 31) has("android.permission.BLUETOOTH_SCAN") else (fine || coarse) && has(Manifest.permission.BLUETOOTH_ADMIN)
        add(Channel.BLUETOOTH, bt, if (Build.VERSION.SDK_INT >= 31) "android:bluetooth_scan" else null)
        add(Channel.CELL, fine, AppOpsManager.OPSTR_FINE_LOCATION)
        add(Channel.PHONE, has(Manifest.permission.READ_PHONE_STATE), null)

        val route = routes[info.packageName]
        val viaVpn = when (route) {
            null -> allVia
            "direct", "block" -> false
            else -> true
        }
        return AppExposure(
            info.packageName, pm.getApplicationLabel(app).toString(),
            app.flags and ApplicationInfo.FLAG_SYSTEM != 0, route, viaVpn, channels, blocked,
        )
    }

    fun appSettingsIntent(pkg: String) =
        Intent(Settings.ACTION_APPLICATION_DETAILS_SETTINGS, Uri.parse("package:$pkg"))
}
