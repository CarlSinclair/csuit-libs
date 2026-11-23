package com.csuit.shell

data class ShellConfig(
    val appName: String,
    val hostPrefix: String,
    val hostSuffix: String,
    val pushChannelName: String,
    val youTubeSync: Boolean = false,
    val mediaScrobble: Boolean = false,
    val nativeNav: Boolean = false,
    val bottomNavPaths: List<String> = emptyList(),
    val scrobbleServices: Map<String, String> = ScrobbleServices.DEFAULT,
) {
    init {
        require(hostPrefix == "https://") { "hostPrefix must be https://" }
        require(hostSuffix.startsWith(".") && hostSuffix.length > 1) { "hostSuffix must start with a dot" }
        require(hostSuffix.none { it == '/' || it == ':' || it == '@' || it == '?' || it == '#' || it.isWhitespace() }) { "hostSuffix must be a bare domain" }
    }

    fun baseUrl(subdomain: String): String = hostPrefix + subdomain + hostSuffix

    fun host(subdomain: String): String = subdomain + hostSuffix
}

object ScrobbleServices {
    val DEFAULT: Map<String, String> = mapOf(
        "com.netflix.mediaclient" to "Netflix",
        "com.amazon.avod.thirdpartyclient" to "Prime Video",
        "com.disney.disneyplus" to "Disney+",
        "com.wbd.stream" to "Max",
        "com.hulu.plus" to "Hulu",
        "com.apple.atve.androidtv.appletv" to "Apple TV",
        "com.crunchyroll.crunchyroid" to "Crunchyroll",
        "com.plexapp.android" to "Plex",
        "org.jellyfin.mobile" to "Jellyfin",
        "com.google.android.youtube" to "YouTube",
    )
}
