package app.kordo.android

import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow

enum class Status { OFF, CONNECTING, CONNECTED, STOPPING }

/** VPN state shared by the service and the UI (same process). */
object VpnState {
    private val _status = MutableStateFlow(Status.OFF)
    val status: StateFlow<Status> = _status
    private val _error = MutableStateFlow<String?>(null)
    val error: StateFlow<String?> = _error

    /** Called by the UI after servers, apps or settings changed. */
    @Volatile
    var reload: (() -> Unit)? = null

    fun set(s: Status, err: String? = null) {
        _status.value = s
        if (err != null || s == Status.CONNECTING) _error.value = err
    }

    fun changed() {
        if (_status.value == Status.CONNECTED) reload?.invoke()
    }
}
