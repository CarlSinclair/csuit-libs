package com.csuit.shell

import android.content.Context
import android.content.pm.PackageManager
import androidx.appcompat.app.AlertDialog
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.suspendCancellableCoroutine
import kotlinx.coroutines.withTimeoutOrNull
import org.json.JSONObject
import org.unifiedpush.android.connector.UnifiedPush
import kotlin.coroutines.resume

class PushPlugin : ShellPlugin {
    override val id = "push"

    override suspend fun handle(host: PluginHost, action: String, args: JSONObject): JSONObject = when (action) {
        "register" -> register(host)
        "state" -> PushState.snapshot(host.activity)
        "ack" -> {
            PushState.markSent(host.activity)
            JSONObject()
        }
        else -> throw PluginException("unknown_action")
    }

    private suspend fun register(host: PluginHost): JSONObject {
        val activity = host.activity
        activity.runOnUiThread { activity.ensureNotificationPermission() }
        if (UnifiedPush.getSavedDistributor(activity) == null) {
            val distributors = UnifiedPush.getDistributors(activity)
            if (distributors.isEmpty()) throw PluginException("no_distributor")
            val pick = if (distributors.size == 1) distributors[0] else choose(activity, distributors) ?: throw PluginException("cancelled")
            UnifiedPush.saveDistributor(activity, pick)
        }
        val waiting = PushState.begin()
        UnifiedPush.register(activity, PushState.INSTANCE)
        val endpoint = withTimeoutOrNull(20_000) { waiting.await() } ?: throw PluginException("timeout")
        if (!endpoint.has("p256dh")) throw PluginException("no_keys")
        PushState.markSent(activity)
        return endpoint
    }

    private suspend fun choose(activity: MainActivity, packages: List<String>): String? =
        suspendCancellableCoroutine { cont ->
            val labels = packages.map { label(activity, it) }.toTypedArray()
            activity.runOnUiThread {
                val dialog = AlertDialog.Builder(activity)
                    .setTitle(R.string.shell_choose_push_app)
                    .setItems(labels) { _, which -> if (cont.isActive) cont.resume(packages[which]) }
                    .setNegativeButton(R.string.shell_cancel) { _, _ -> if (cont.isActive) cont.resume(null) }
                    .setOnCancelListener { if (cont.isActive) cont.resume(null) }
                    .show()
                cont.invokeOnCancellation { dialog.dismiss() }
            }
        }

    private fun label(context: Context, pkg: String): String = try {
        val info = context.packageManager.getApplicationInfo(pkg, 0)
        context.packageManager.getApplicationLabel(info).toString()
    } catch (e: PackageManager.NameNotFoundException) {
        pkg
    }
}

object PushState {
    const val INSTANCE = "default"
    private const val PREFS = "shell_push"
    private var waiting: CompletableDeferred<JSONObject>? = null

    fun begin(): CompletableDeferred<JSONObject> {
        val d = CompletableDeferred<JSONObject>()
        waiting = d
        return d
    }

    private fun b64url(s: String): String = s.replace('+', '-').replace('/', '_').trimEnd('=')

    fun onEndpoint(context: Context, url: String, pubKey: String?, auth: String?) {
        val json = JSONObject().put("endpoint", url)
        if (pubKey != null && auth != null) {
            json.put("p256dh", b64url(pubKey)).put("auth", b64url(auth))
        }
        context.getSharedPreferences(PREFS, Context.MODE_PRIVATE).edit()
            .putString("endpoint", json.toString())
            .putBoolean("pending", true)
            .apply()
        waiting?.complete(json)
        waiting = null
    }

    fun onFailed() {
        waiting?.completeExceptionally(PluginException("registration_failed"))
        waiting = null
    }

    fun onUnregistered(context: Context) {
        context.getSharedPreferences(PREFS, Context.MODE_PRIVATE).edit().clear().apply()
    }

    fun markSent(context: Context) {
        context.getSharedPreferences(PREFS, Context.MODE_PRIVATE).edit().putBoolean("pending", false).apply()
    }

    fun snapshot(context: Context): JSONObject {
        val prefs = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
        val stored = prefs.getString("endpoint", null) ?: return JSONObject().put("registered", false).put("pending", false)
        val out = JSONObject(stored)
        return out.put("registered", true).put("pending", prefs.getBoolean("pending", false))
    }
}
