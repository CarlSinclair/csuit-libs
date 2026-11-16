package com.csuit.shell

import android.content.ComponentName
import android.media.MediaMetadata
import android.media.session.MediaController
import android.media.session.MediaSessionManager
import android.media.session.PlaybackState
import android.os.Handler
import android.os.Looper
import android.os.SystemClock
import android.service.notification.NotificationListenerService
import org.json.JSONObject
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale
import java.util.TimeZone
import java.util.UUID

class ScrobbleListener : NotificationListenerService() {
    private val handler = Handler(Looper.getMainLooper())
    private val tracked = mutableMapOf<String, Tracker>()
    private var manager: MediaSessionManager? = null

    private val sessionsListener = MediaSessionManager.OnActiveSessionsChangedListener { controllers ->
        attach(controllers ?: emptyList())
    }

    override fun onListenerConnected() {
        val msm = getSystemService(MediaSessionManager::class.java)
        manager = msm
        val component = ComponentName(this, ScrobbleListener::class.java)
        try {
            msm.addOnActiveSessionsChangedListener(sessionsListener, component, handler)
            attach(msm.getActiveSessions(component))
        } catch (e: SecurityException) {
            android.util.Log.e("Shell", "media session access refused", e)
        }
    }

    override fun onListenerDisconnected() {
        manager?.removeOnActiveSessionsChangedListener(sessionsListener)
        tracked.values.forEach { it.stop() }
        tracked.clear()
    }

    private fun attach(controllers: List<MediaController>) {
        if (!Shell.config.mediaScrobble) return
        val services = Shell.config.scrobbleServices
        val live = controllers.filter { it.packageName in services }.associateBy { it.packageName }
        tracked.keys.filter { it !in live }.forEach { tracked.remove(it)?.stop() }
        live.forEach { (pkg, controller) ->
            if (tracked[pkg]?.controller?.sessionToken != controller.sessionToken) {
                tracked.remove(pkg)?.stop()
                tracked[pkg] = Tracker(controller, services.getValue(pkg)).also { it.start() }
            }
        }
    }

    private inner class Tracker(val controller: MediaController, val service: String) : MediaController.Callback() {
        private var key = ""
        private var reported = false
        private var title = ""
        private var subtitle = ""
        private var album = ""
        private var artist = ""
        private var durationMs = 0L
        private var playingSince = 0L
        private var playedMs = 0L
        private val tick = object : Runnable {
            override fun run() {
                check()
                handler.postDelayed(this, 30_000)
            }
        }

        fun start() {
            controller.registerCallback(this, handler)
            onMetadataChanged(controller.metadata)
            onPlaybackStateChanged(controller.playbackState)
            handler.postDelayed(tick, 30_000)
        }

        fun stop() {
            handler.removeCallbacks(tick)
            controller.unregisterCallback(this)
        }

        override fun onMetadataChanged(metadata: MediaMetadata?) {
            val t = metadata?.getString(MediaMetadata.METADATA_KEY_TITLE).orEmpty()
            val sub = metadata?.getString(MediaMetadata.METADATA_KEY_DISPLAY_SUBTITLE).orEmpty()
            val alb = metadata?.getString(MediaMetadata.METADATA_KEY_ALBUM).orEmpty()
            val art = metadata?.getString(MediaMetadata.METADATA_KEY_ARTIST).orEmpty()
            val next = "$t|$sub|$alb|$art"
            if (next != key) {
                key = next
                reported = false
                title = t
                subtitle = sub
                album = alb
                artist = art
                playedMs = 0
                playingSince = if (isPlaying(controller.playbackState)) SystemClock.elapsedRealtime() else 0
            }
            durationMs = metadata?.getLong(MediaMetadata.METADATA_KEY_DURATION) ?: 0
        }

        override fun onPlaybackStateChanged(state: PlaybackState?) {
            val playing = isPlaying(state)
            val now = SystemClock.elapsedRealtime()
            if (playing && playingSince == 0L) {
                playingSince = now
            } else if (!playing && playingSince != 0L) {
                playedMs += now - playingSince
                playingSince = 0
            }
            check()
        }

        private fun isPlaying(state: PlaybackState?) = state?.state == PlaybackState.STATE_PLAYING

        private fun position(): Long {
            val state = controller.playbackState ?: return 0
            if (state.state != PlaybackState.STATE_PLAYING) return state.position
            val delta = SystemClock.elapsedRealtime() - state.lastPositionUpdateTime
            return state.position + (delta * state.playbackSpeed).toLong()
        }

        private fun check() {
            if (reported || title.isEmpty()) return
            val played = playedMs + if (playingSince != 0L) SystemClock.elapsedRealtime() - playingSince else 0
            val finished = if (durationMs > 0) position() >= durationMs * 8 / 10 && played >= 60_000 else played >= 20 * 60_000
            if (!finished) return
            reported = true
            val format = SimpleDateFormat("yyyy-MM-dd'T'HH:mm:ss'Z'", Locale.US).apply { timeZone = TimeZone.getTimeZone("UTC") }
            ScrobbleQueue.add(
                this@ScrobbleListener,
                JSONObject()
                    .put("id", UUID.randomUUID().toString())
                    .put("service", service)
                    .put("title", title)
                    .put("subtitle", subtitle)
                    .put("album", album)
                    .put("artist", artist)
                    .put("duration_ms", durationMs)
                    .put("watched_at", format.format(Date())),
            )
        }
    }
}
