package com.csuit.shell

import android.app.Application

object Shell {
    private var installed: ShellConfig? = null
    private lateinit var app: Application

    val config: ShellConfig
        get() = installed ?: error("Shell.install must be called from Application.onCreate")

    val application: Application
        get() = app

    fun install(application: Application, config: ShellConfig) {
        app = application
        installed = config
    }

    fun plugins(): List<ShellPlugin> {
        val list = mutableListOf<ShellPlugin>(PushPlugin(), InstancePlugin())
        if (config.youTubeSync) list.add(YouTubeHistoryPlugin())
        if (config.mediaScrobble) list.add(ScrobblePlugin())
        return list
    }
}
