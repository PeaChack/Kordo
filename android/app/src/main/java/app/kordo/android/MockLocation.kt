package app.kordo.android

import android.app.AppOpsManager
import android.content.Context
import android.location.Criteria
import android.location.Location
import android.location.LocationManager
import android.location.provider.ProviderProperties
import android.os.Build
import android.os.Handler
import android.os.Looper
import android.os.Process
import android.os.SystemClock
import android.util.Log
import app.kordo.core.mobile.Mobile
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import org.json.JSONObject
import kotlin.random.Random

/**
 * Fake location: while the VPN is on, Kordo is the test provider for GPS,
 * network and fused location and reports the server's place (moved randomly
 * within a radius once per connection). Needs Kordo to be selected as the
 * "mock location app" in Developer options. Affects every app on the phone.
 */
object MockLocation {
    data class State(val active: Boolean = false, val point: String? = null, val error: String? = null)

    private val _state = MutableStateFlow(State())
    val state: StateFlow<State> = _state

    private val handler = Handler(Looper.getMainLooper())
    private var lm: LocationManager? = null
    private var providers: List<String> = emptyList()
    private var lat = 0.0
    private var lon = 0.0
    private var accuracy = 12f

    /** Kordo is the mock location app in Developer options. */
    fun allowed(ctx: Context): Boolean {
        val ops = ctx.getSystemService(AppOpsManager::class.java)
        return runCatching {
            checkOp(ops, AppOpsManager.OPSTR_MOCK_LOCATION, Process.myUid(), ctx.packageName) == AppOpsManager.MODE_ALLOWED
        }.getOrDefault(false)
    }

    fun enabledInSettings(): Boolean = runCatching { JSONObject(Mobile.privacySettings()).optBoolean("mockLocation") }.getOrDefault(false)

    /** Called by the VPN service after it connected. Blocking network lookup: call off the main thread. */
    fun start(ctx: Context) {
        if (!enabledInSettings()) return
        if (!allowed(ctx)) {
            _state.value = State(error = "Kordo не выбран в «Для разработчиков» → «Приложение для фиктивных местоположений»")
            return
        }
        val point = runCatching { JSONObject(Mobile.mockPoint()) }.getOrElse {
            _state.value = State(error = "Не удалось определить место сервера: ${it.message}")
            return
        }
        lat = point.getDouble("lat")
        lon = point.getDouble("lon")
        accuracy = 8f + Random.nextFloat() * 10f
        val manager = ctx.getSystemService(LocationManager::class.java)
        val names = buildList {
            add(LocationManager.GPS_PROVIDER)
            add(LocationManager.NETWORK_PROVIDER)
            if (Build.VERSION.SDK_INT >= 31) add(LocationManager.FUSED_PROVIDER)
        }
        val added = names.filter { addProvider(manager, it) }
        if (added.isEmpty()) {
            _state.value = State(error = "Android не дал зарегистрировать тестовый источник местоположения")
            return
        }
        handler.post {
            lm = manager
            providers = added
            _state.value = State(active = true, point = "%.4f, %.4f%s".format(lat, lon,
                point.optString("city").takeIf { it.isNotEmpty() }?.let { " ($it)" } ?: ""))
            handler.post(tick)
        }
    }

    fun stop() {
        handler.post {
            handler.removeCallbacks(tick)
            val manager = lm
            providers.forEach { p -> runCatching { manager?.removeTestProvider(p) } }
            providers = emptyList()
            lm = null
            _state.value = State()
        }
    }

    private val tick = object : Runnable {
        override fun run() {
            val manager = lm ?: return
            for (p in providers) {
                val l = Location(p).apply {
                    // a stationary phone: the fix wanders by a few metres
                    latitude = lat + (Random.nextDouble() - 0.5) * 0.00004
                    longitude = lon + (Random.nextDouble() - 0.5) * 0.00006
                    altitude = 15.0 + Random.nextDouble() * 4
                    accuracy = this@MockLocation.accuracy + Random.nextFloat() * 3
                    speed = 0f
                    bearing = 0f
                    time = System.currentTimeMillis()
                    elapsedRealtimeNanos = SystemClock.elapsedRealtimeNanos()
                    if (Build.VERSION.SDK_INT >= 26) {
                        verticalAccuracyMeters = 6f
                        speedAccuracyMetersPerSecond = 0.5f
                        bearingAccuracyDegrees = 30f
                    }
                }
                runCatching { manager.setTestProviderLocation(p, l) }.onFailure { Log.w("Kordo", "mock $p", it) }
            }
            handler.postDelayed(this, 1000)
        }
    }

    @Suppress("DEPRECATION")
    private fun addProvider(m: LocationManager, name: String): Boolean = runCatching {
        runCatching { m.removeTestProvider(name) }
        if (Build.VERSION.SDK_INT >= 31) {
            m.addTestProvider(
                name, false, false, false, false, true, true, true,
                ProviderProperties.POWER_USAGE_LOW, ProviderProperties.ACCURACY_FINE,
            )
        } else {
            @Suppress("WrongConstant") // the same numbers as ProviderProperties, which needs API 31
            m.addTestProvider(name, false, false, false, false, true, true, true, Criteria.POWER_LOW, Criteria.ACCURACY_FINE)
        }
        m.setTestProviderEnabled(name, true)
        true
    }.getOrElse {
        Log.w("Kordo", "addTestProvider $name", it)
        false
    }
}

/** AppOps check that works on every supported Android version. */
internal fun checkOp(ops: AppOpsManager, op: String, uid: Int, pkg: String): Int =
    if (Build.VERSION.SDK_INT >= 29) ops.unsafeCheckOpNoThrow(op, uid, pkg)
    else @Suppress("DEPRECATION") ops.checkOpNoThrow(op, uid, pkg)
