package app.kordo.android

import android.content.Context
import android.content.Intent
import android.net.Uri
import android.provider.Settings
import androidx.core.content.FileProvider
import app.kordo.core.mobile.Mobile
import org.json.JSONObject
import java.io.File
import java.net.HttpURLConnection
import java.net.URL
import java.security.MessageDigest

data class UpdateInfo(val current: String, val latest: String, val available: Boolean, val apk: String, val sha256: String, val page: String)

/** Updates from GitHub releases: the APK is checked against the release's SHA-256, Android checks the signature. */
object Updater {
    fun check(): UpdateInfo {
        val j = JSONObject(Mobile.checkUpdate())
        return UpdateInfo(
            j.optString("current"), j.optString("latest"), j.optBoolean("available"),
            j.optString("apk"), j.optString("sha256"), j.optString("page"),
        )
    }

    /** Downloads and verifies the APK; returns the file. Blocking. */
    fun download(context: Context, info: UpdateInfo): File {
        val dir = File(context.cacheDir, "updates").apply { mkdirs() }
        dir.listFiles()?.forEach { it.delete() }
        val file = File(dir, "Kordo-${info.latest}.apk")
        val md = MessageDigest.getInstance("SHA-256")
        val conn = URL(info.apk).openConnection() as HttpURLConnection
        conn.instanceFollowRedirects = true
        conn.connectTimeout = 20_000
        conn.readTimeout = 60_000
        conn.inputStream.use { input ->
            file.outputStream().use { out ->
                val buf = ByteArray(64 * 1024)
                while (true) {
                    val n = input.read(buf)
                    if (n < 0) break
                    md.update(buf, 0, n)
                    out.write(buf, 0, n)
                }
            }
        }
        val got = md.digest().joinToString("") { "%02x".format(it) }
        if (got != info.sha256) {
            file.delete()
            throw IllegalStateException("контрольная сумма не совпала, файл удалён")
        }
        return file
    }

    /** Hands the APK to the system installer (asks for "install unknown apps" first if needed). */
    fun install(context: Context, apk: File) {
        if (!context.packageManager.canRequestPackageInstalls()) {
            context.startActivity(
                Intent(Settings.ACTION_MANAGE_UNKNOWN_APP_SOURCES, Uri.parse("package:${context.packageName}"))
                    .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
            )
            return
        }
        val uri = FileProvider.getUriForFile(context, "${context.packageName}.files", apk)
        context.startActivity(
            Intent(Intent.ACTION_VIEW).setDataAndType(uri, "application/vnd.android.package-archive")
                .addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_ACTIVITY_NEW_TASK)
        )
    }
}
