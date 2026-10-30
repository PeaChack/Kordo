package app.kordo.android

import android.app.Application
import android.app.NotificationChannel
import android.app.NotificationManager
import app.kordo.core.libbox.Libbox
import app.kordo.core.libbox.SetupOptions
import app.kordo.core.mobile.Mobile
import java.io.File

class KordoApp : Application() {
    override fun onCreate() {
        super.onCreate()
        instance = this
        Mobile.init(filesDir.path)
        Libbox.setup(SetupOptions().apply {
            basePath = filesDir.path
            workingPath = File(filesDir, "working").path
            tempPath = cacheDir.path
            appVersion = BuildConfig.VERSION_NAME
            crashReportSource = "kordo"
        })
        val nm = getSystemService(NotificationManager::class.java)
        nm.createNotificationChannel(
            NotificationChannel(CHANNEL_VPN, "VPN", NotificationManager.IMPORTANCE_LOW)
        )
    }

    companion object {
        const val CHANNEL_VPN = "vpn"
        lateinit var instance: KordoApp
            private set
    }
}
