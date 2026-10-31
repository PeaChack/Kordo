package app.kordo.android

import android.app.Notification as AndroidNotification
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.net.ConnectivityManager
import android.net.LinkProperties
import android.net.Network
import android.net.NetworkCapabilities
import android.net.NetworkRequest
import android.net.ProxyInfo
import android.net.VpnService
import android.os.Build
import android.os.ParcelFileDescriptor
import android.os.Process
import android.system.OsConstants
import android.util.Log
import androidx.core.app.NotificationCompat
import app.kordo.core.libbox.BridgeOptions
import app.kordo.core.libbox.BridgeSession
import app.kordo.core.libbox.CommandServer
import app.kordo.core.libbox.CommandServerHandler
import app.kordo.core.libbox.ConnectionOwner
import app.kordo.core.libbox.InterfaceUpdateListener
import app.kordo.core.libbox.Libbox
import app.kordo.core.libbox.LocalDNSTransport
import app.kordo.core.libbox.NeighborUpdateListener
import app.kordo.core.libbox.NetworkInterface as BoxInterface
import app.kordo.core.libbox.NetworkInterfaceIterator
import app.kordo.core.libbox.Notification
import app.kordo.core.libbox.OverrideOptions
import app.kordo.core.libbox.PlatformInterface
import app.kordo.core.libbox.PlatformUser
import app.kordo.core.libbox.ShellSession
import app.kordo.core.libbox.StringIterator
import app.kordo.core.libbox.SystemProxyStatus
import app.kordo.core.libbox.TunOptions
import app.kordo.core.libbox.WIFIState
import app.kordo.core.mobile.Mobile
import java.net.Inet6Address
import java.net.InetSocketAddress
import java.net.NetworkInterface

/**
 * The VPN: a foreground VpnService that runs sing-box (libbox) with the
 * config built by the Go core. It is libbox's PlatformInterface: it opens the
 * TUN, protects sockets of the engine, tells which app owns a connection and
 * reports the default network.
 */
class KordoVpnService : VpnService(), PlatformInterface, CommandServerHandler {

    private var server: CommandServer? = null
    private var tun: ParcelFileDescriptor? = null
    private val connectivity by lazy { getSystemService(ConnectivityManager::class.java) }
    private var networkCallback: ConnectivityManager.NetworkCallback? = null
    private var interfaceListener: InterfaceUpdateListener? = null

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (intent?.action == ACTION_STOP) {
            stopVpn()
            return START_NOT_STICKY
        }
        startForegroundNotification()
        if (server == null) startVpn()
        return START_STICKY
    }

    override fun onRevoke() = stopVpn() // another VPN took over or the user revoked access

    override fun onDestroy() {
        stopVpn()
        super.onDestroy()
    }

    private fun startVpn() {
        VpnState.set(Status.CONNECTING)
        VpnState.reload = { reloadConfig() }
        Thread {
            try {
                val config = Mobile.buildConfig(hasGlobalIPv6())
                val s = Libbox.newCommandServer(this, this)
                s.start()
                server = s
                s.startOrReloadService(config, overrides())
                VpnState.set(Status.CONNECTED)
            } catch (e: Exception) {
                Log.e(TAG, "start failed", e)
                VpnState.set(Status.OFF, e.message ?: e.toString())
                stopVpn(keepError = true)
            }
        }.start()
    }

    private fun reloadConfig() {
        val s = server ?: return
        Thread {
            try {
                s.startOrReloadService(Mobile.buildConfig(hasGlobalIPv6()), overrides())
            } catch (e: Exception) {
                Log.e(TAG, "reload failed", e)
                VpnState.set(Status.CONNECTED, e.message)
            }
        }.start()
    }

    private fun overrides() = OverrideOptions().apply {
        includePackage = StringArrayIterator(emptyList())
        excludePackage = StringArrayIterator(emptyList())
    }

    private fun stopVpn(keepError: Boolean = false) {
        if (!keepError) VpnState.set(Status.STOPPING)
        VpnState.reload = null
        val s = server
        server = null
        try {
            s?.closeService()
            s?.close()
        } catch (e: Exception) {
            Log.w(TAG, "close", e)
        }
        tun?.close()
        tun = null
        stopForeground(STOP_FOREGROUND_REMOVE)
        stopSelf()
        if (!keepError) VpnState.set(Status.OFF)
    }

    private fun startForegroundNotification() {
        val open = PendingIntent.getActivity(
            this, 0, Intent(this, MainActivity::class.java), PendingIntent.FLAG_IMMUTABLE
        )
        val stop = PendingIntent.getService(
            this, 1, Intent(this, KordoVpnService::class.java).setAction(ACTION_STOP), PendingIntent.FLAG_IMMUTABLE
        )
        val n: AndroidNotification = NotificationCompat.Builder(this, KordoApp.CHANNEL_VPN)
            .setSmallIcon(R.drawable.ic_stat_kordo)
            .setContentTitle("Kordo")
            .setContentText("VPN включён")
            .setContentIntent(open)
            .addAction(0, "Отключить", stop)
            .setOngoing(true)
            .build()
        if (Build.VERSION.SDK_INT >= 34) {
            startForeground(NOTIFICATION_ID, n, ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE)
        } else {
            startForeground(NOTIFICATION_ID, n)
        }
    }

    private fun hasGlobalIPv6(): Boolean {
        val lp = connectivity.getLinkProperties(connectivity.activeNetwork) ?: return false
        return lp.linkAddresses.any { a ->
            val ip = a.address
            ip is Inet6Address && !ip.isLinkLocalAddress && !ip.isLoopbackAddress && !ip.isSiteLocalAddress
        }
    }

    // ------------------------------------------------------------ CommandServerHandler

    override fun serviceStop() = stopVpn()
    override fun serviceReload() = reloadConfig()
    override fun getSystemProxyStatus(): SystemProxyStatus = SystemProxyStatus()
    override fun setSystemProxyEnabled(enabled: Boolean) {}
    override fun triggerNativeCrash() {}
    override fun writeDebugMessage(message: String) {
        Log.d(TAG, message)
    }
    override fun connectSSHAgent(): Int {
        throw UnsupportedOperationException("no SSH agent")
    }

    // ------------------------------------------------------------ PlatformInterface

    override fun localDNSTransport(): LocalDNSTransport? = null

    override fun usePlatformAutoDetectInterfaceControl(): Boolean = true

    override fun autoDetectInterfaceControl(fd: Int) {
        if (!protect(fd)) throw IllegalStateException("protect($fd) failed")
    }

    override fun openTun(options: TunOptions): Int {
        if (prepare(this) != null) throw IllegalStateException("VPN permission is missing")
        val b = Builder().setSession("Kordo").setMtu(options.mtu)
        options.inet4Address.forEachPrefix { a, p -> b.addAddress(a, p) }
        options.inet6Address.forEachPrefix { a, p -> b.addAddress(a, p) }
        if (options.autoRoute) {
            options.dnsServerAddress.toList().forEach { b.addDnsServer(it) }
            var routes4 = 0
            var routes6 = 0
            options.inet4RouteRange.forEachPrefix { a, p -> b.addRoute(a, p); routes4++ }
            options.inet6RouteRange.forEachPrefix { a, p -> b.addRoute(a, p); routes6++ }
            if (routes4 == 0 && options.inet4Address.hasNext()) b.addRoute("0.0.0.0", 0)
            if (routes6 == 0 && options.inet6Address.hasNext()) b.addRoute("::", 0)
            val include = options.includePackage.toList()
            val exclude = options.excludePackage.toList()
            if (include.isNotEmpty()) {
                include.forEach { runCatching { b.addAllowedApplication(it) } }
            } else {
                // Kordo's own traffic (update checks) never loops through the tunnel
                (exclude + packageName).distinct().forEach { runCatching { b.addDisallowedApplication(it) } }
            }
        }
        if (options.isHTTPProxyEnabled && Build.VERSION.SDK_INT >= 29) {
            b.setHttpProxy(ProxyInfo.buildDirectProxy(options.httpProxyServer, options.httpProxyServerPort))
        }
        if (Build.VERSION.SDK_INT >= 29) b.setMetered(false)
        val pfd = b.establish() ?: throw IllegalStateException("VPN permission is missing")
        tun?.close()
        tun = pfd
        return pfd.fd
    }

    override fun findConnectionOwner(
        ipProtocol: Int, sourceAddress: String, sourcePort: Int,
        destinationAddress: String, destinationPort: Int,
    ): ConnectionOwner {
        val uid = if (Build.VERSION.SDK_INT >= 29) {
            connectivity.getConnectionOwnerUid(
                ipProtocol,
                InetSocketAddress(sourceAddress, sourcePort),
                InetSocketAddress(destinationAddress, destinationPort),
            )
        } else {
            Libbox.findConnectionOwner(ipProtocol, sourceAddress, sourcePort, destinationAddress, destinationPort).userId
        }
        if (uid == Process.INVALID_UID) throw IllegalStateException("connection owner not found")
        val packages = packageManager.getPackagesForUid(uid)?.toList() ?: emptyList()
        return ConnectionOwner().apply {
            userId = uid
            userName = packages.firstOrNull() ?: ""
            setAndroidPackageNames(StringArrayIterator(packages))
        }
    }

    override fun startDefaultInterfaceMonitor(listener: InterfaceUpdateListener) {
        interfaceListener = listener
        val cb = object : ConnectivityManager.NetworkCallback() {
            override fun onAvailable(network: Network) = report(network)
            override fun onCapabilitiesChanged(network: Network, caps: NetworkCapabilities) = report(network)
            override fun onLinkPropertiesChanged(network: Network, lp: LinkProperties) = report(network)
            override fun onLost(network: Network) {
                listener.updateDefaultInterface("", -1, false, false)
            }
        }
        networkCallback = cb
        val request = NetworkRequest.Builder()
            .addCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)
            .addCapability(NetworkCapabilities.NET_CAPABILITY_NOT_VPN)
            .build()
        if (Build.VERSION.SDK_INT >= 28) {
            connectivity.requestNetwork(request, cb)
        } else {
            connectivity.registerNetworkCallback(request, cb)
        }
        connectivity.activeNetwork?.let { report(it) }
    }

    private fun report(network: Network) {
        val listener = interfaceListener ?: return
        val lp = connectivity.getLinkProperties(network) ?: return
        val caps = connectivity.getNetworkCapabilities(network)
        if (caps != null && caps.hasTransport(NetworkCapabilities.TRANSPORT_VPN)) return
        val name = lp.interfaceName ?: return
        val index = runCatching { NetworkInterface.getByName(name)?.index }.getOrNull() ?: return
        val metered = caps?.hasCapability(NetworkCapabilities.NET_CAPABILITY_NOT_METERED) == false
        listener.updateDefaultInterface(name, index, metered, false)
    }

    override fun closeDefaultInterfaceMonitor(listener: InterfaceUpdateListener) {
        networkCallback?.let { runCatching { connectivity.unregisterNetworkCallback(it) } }
        networkCallback = null
        interfaceListener = null
    }

    override fun getInterfaces(): NetworkInterfaceIterator {
        val out = mutableListOf<BoxInterface>()
        for (network in connectivity.allNetworks) {
            val lp = connectivity.getLinkProperties(network) ?: continue
            val caps = connectivity.getNetworkCapabilities(network) ?: continue
            if (caps.hasTransport(NetworkCapabilities.TRANSPORT_VPN)) continue
            val name = lp.interfaceName ?: continue
            val ni = runCatching { NetworkInterface.getByName(name) }.getOrNull() ?: continue
            out.add(BoxInterface().apply {
                setName(name)
                index = ni.index
                mtu = runCatching { ni.mtu }.getOrDefault(1500)
                addresses = StringArrayIterator(lp.linkAddresses.map { "${it.address.hostAddress?.substringBefore('%')}/${it.prefixLength}" })
                var f = 0
                if (ni.isUp) f = f or OsConstants.IFF_UP or OsConstants.IFF_RUNNING
                if (ni.isLoopback) f = f or OsConstants.IFF_LOOPBACK
                if (ni.isPointToPoint) f = f or OsConstants.IFF_POINTOPOINT
                if (ni.supportsMulticast()) f = f or OsConstants.IFF_MULTICAST
                flags = f
                type = when {
                    caps.hasTransport(NetworkCapabilities.TRANSPORT_WIFI) -> Libbox.InterfaceTypeWIFI
                    caps.hasTransport(NetworkCapabilities.TRANSPORT_CELLULAR) -> Libbox.InterfaceTypeCellular
                    caps.hasTransport(NetworkCapabilities.TRANSPORT_ETHERNET) -> Libbox.InterfaceTypeEthernet
                    else -> Libbox.InterfaceTypeOther
                }
                dnsServer = StringArrayIterator(lp.dnsServers.mapNotNull { it.hostAddress })
                gateway = StringArrayIterator(lp.routes.filter { it.isDefaultRoute && it.gateway != null }.mapNotNull { it.gateway?.hostAddress })
                metered = !caps.hasCapability(NetworkCapabilities.NET_CAPABILITY_NOT_METERED)
            })
        }
        return InterfaceArrayIterator(out)
    }

    override fun underNetworkExtension(): Boolean = false
    override fun includeAllNetworks(): Boolean = false
    override fun readWIFIState(): WIFIState? = null
    override fun clearDNSCache() {}
    override fun sendNotification(notification: Notification) {}
    override fun cancelNotification(identifier: String, typeID: Int) {}
    override fun startNeighborMonitor(listener: NeighborUpdateListener) {}
    override fun closeNeighborMonitor(listener: NeighborUpdateListener) {}
    override fun registerMyInterface(name: String) {}
    override fun usePlatformShell(): Boolean = false
    override fun checkPlatformShell() {
        throw UnsupportedOperationException("no shell")
    }
    override fun openShellSession(
        user: PlatformUser, command: String, environ: StringIterator, term: String, rows: Int, cols: Int,
    ): ShellSession {
        throw UnsupportedOperationException("no shell")
    }
    override fun lookupUser(username: String): PlatformUser {
        throw UnsupportedOperationException("no users")
    }
    override fun lookupSFTPServer(): String {
        throw UnsupportedOperationException("no sftp")
    }
    override fun readSystemSSHHostKey(): String {
        throw UnsupportedOperationException("no ssh")
    }
    override fun tailscaleHostname(): String = ""
    override fun usePlatformBridge(): Boolean = false
    override fun createBridge(options: BridgeOptions): BridgeSession {
        throw UnsupportedOperationException("no bridge")
    }

    companion object {
        private const val TAG = "Kordo"
        private const val NOTIFICATION_ID = 1
        const val ACTION_STOP = "app.kordo.android.STOP"

        fun start(context: Context) {
            context.startForegroundService(Intent(context, KordoVpnService::class.java))
        }

        fun stop(context: Context) {
            context.startService(Intent(context, KordoVpnService::class.java).setAction(ACTION_STOP))
        }
    }
}
