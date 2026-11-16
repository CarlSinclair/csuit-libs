package com.csuit.shell

import android.content.Context
import android.content.Intent
import android.provider.Settings
import androidx.core.app.NotificationManagerCompat
import org.json.JSONArray
import org.json.JSONObject

object ScrobbleQueue {
    private const val PREFS = "shell_scrobble"
    private const val MAX = 200
    private val lock = Any()

    private fun prefs(context: Context) = context.applicationContext.getSharedPreferences(PREFS, Context.MODE_PRIVATE)

    fun add(context: Context, event: JSONObject) {
        synchronized(lock) {
            val list = read(context)
            list.add(event)
            while (list.size > MAX) list.removeAt(0)
            write(context, list)
        }
    }

    fun all(context: Context): List<JSONObject> = synchronized(lock) { read(context) }

    fun remove(context: Context, ids: Set<String>) {
        synchronized(lock) {
            write(context, read(context).filter { it.optString("id") !in ids }.toMutableList())
        }
    }

    private fun read(context: Context): MutableList<JSONObject> {
        val raw = prefs(context).getString("queue", "[]") ?: "[]"
        val array = try {
            JSONArray(raw)
        } catch (e: Exception) {
            android.util.Log.e("Shell", "scrobble queue unreadable, dropping it", e)
            JSONArray()
        }
        return (0 until array.length()).map { array.getJSONObject(it) }.toMutableList()
    }

    private fun write(context: Context, list: List<JSONObject>) {
        prefs(context).edit().putString("queue", JSONArray(list).toString()).apply()
    }
}

class ScrobblePlugin : ShellPlugin {
    override val id = "scrobble"

    override suspend fun handle(host: PluginHost, action: String, args: JSONObject): JSONObject {
        val ctx = host.activity
        return when (action) {
            "status" -> JSONObject()
                .put("access", NotificationManagerCompat.getEnabledListenerPackages(ctx).contains(ctx.packageName))
                .put("services", JSONArray(Shell.config.scrobbleServices.values.toList()))
            "open_access" -> {
                ctx.runOnUiThread { ctx.startActivity(Intent(Settings.ACTION_NOTIFICATION_LISTENER_SETTINGS)) }
                JSONObject()
            }
            "drain" -> JSONObject().put("events", JSONArray(ScrobbleQueue.all(ctx)))
            "ack" -> {
                val ids = args.optJSONArray("ids") ?: JSONArray()
                ScrobbleQueue.remove(ctx, (0 until ids.length()).map { ids.getString(it) }.toSet())
                JSONObject()
            }
            else -> throw PluginException("unknown_action")
        }
    }
}
