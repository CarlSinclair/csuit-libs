package com.csuit.shell

import org.json.JSONObject

class PluginException(val code: String) : Exception(code)

interface PluginHost {
    val activity: MainActivity
}

interface ShellPlugin {
    val id: String

    suspend fun handle(host: PluginHost, action: String, args: JSONObject): JSONObject
}

class InstancePlugin : ShellPlugin {
    override val id = "shell"

    override suspend fun handle(host: PluginHost, action: String, args: JSONObject): JSONObject = when (action) {
        "info" -> JSONObject().put("app", Shell.config.appName).put("platform", "android")
        "change_instance" -> {
            host.activity.runOnUiThread { host.activity.changeInstance() }
            JSONObject()
        }
        else -> throw PluginException("unknown_action")
    }
}
