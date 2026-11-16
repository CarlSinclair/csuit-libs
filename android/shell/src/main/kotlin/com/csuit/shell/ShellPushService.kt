package com.csuit.shell

import android.Manifest
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.os.Build
import androidx.core.app.NotificationCompat
import androidx.core.app.NotificationManagerCompat
import androidx.core.content.ContextCompat
import org.json.JSONObject
import org.unifiedpush.android.connector.FailedReason
import org.unifiedpush.android.connector.PushService
import org.unifiedpush.android.connector.data.PushEndpoint
import org.unifiedpush.android.connector.data.PushMessage

class ShellPushService : PushService() {
    override fun onNewEndpoint(endpoint: PushEndpoint, instance: String) {
        PushState.onEndpoint(this, endpoint.url, endpoint.pubKeySet?.pubKey, endpoint.pubKeySet?.auth)
    }

    override fun onMessage(message: PushMessage, instance: String) {
        Notifier.show(this, String(message.content, Charsets.UTF_8))
    }

    override fun onRegistrationFailed(reason: FailedReason, instance: String) {
        android.util.Log.w("Shell", "push registration failed: $reason")
        PushState.onFailed()
    }

    override fun onUnregistered(instance: String) {
        PushState.onUnregistered(this)
    }
}

object Notifier {
    private const val CHANNEL = "shell_default"

    fun show(context: Context, payload: String) {
        val data = try {
            JSONObject(payload)
        } catch (e: Exception) {
            android.util.Log.w("Shell", "push payload is not JSON", e)
            return
        }
        val title = data.optString("title", Shell.config.appName)
        val body = data.optString("body", "")
        val link = data.optString("link", "")

        if (Build.VERSION.SDK_INT >= 26) {
            val channel = NotificationChannel(CHANNEL, Shell.config.pushChannelName, NotificationManager.IMPORTANCE_DEFAULT)
            context.getSystemService(NotificationManager::class.java).createNotificationChannel(channel)
        }
        if (Build.VERSION.SDK_INT >= 33 &&
            ContextCompat.checkSelfPermission(context, Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED
        ) {
            android.util.Log.w("Shell", "notification permission not granted; push dropped")
            return
        }

        val open = Intent(context, MainActivity::class.java)
            .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_SINGLE_TOP)
            .putExtra(MainActivity.EXTRA_LINK, link)
        val pending = PendingIntent.getActivity(
            context,
            link.hashCode(),
            open,
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE,
        )
        val notification = NotificationCompat.Builder(context, CHANNEL)
            .setSmallIcon(R.drawable.shell_notification_icon)
            .setContentTitle(title)
            .setContentText(body)
            .setStyle(NotificationCompat.BigTextStyle().bigText(body))
            .setAutoCancel(true)
            .setContentIntent(pending)
            .build()
        NotificationManagerCompat.from(context).notify((System.nanoTime() and 0x7FFFFFFF).toInt(), notification)
    }
}
