package com.csuit.shell

import android.content.Context
import java.util.Locale

object Subdomain {
    private val pattern = Regex("^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$")

    fun sanitize(raw: String): String? {
        val value = raw.trim().lowercase(Locale.ROOT)
        return if (pattern.matches(value)) value else null
    }
}

class InstanceStore(context: Context) {
    private val prefs = context.applicationContext.getSharedPreferences("shell_instance", Context.MODE_PRIVATE)

    fun subdomain(): String? = prefs.getString("subdomain", null)?.let { Subdomain.sanitize(it) }

    fun save(subdomain: String) {
        prefs.edit().putString("subdomain", subdomain).apply()
    }

    fun clear() {
        prefs.edit().remove("subdomain").apply()
    }
}
