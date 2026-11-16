package com.csuit.shell

import android.annotation.SuppressLint
import android.content.Context
import android.content.Intent
import android.webkit.CookieManager
import android.webkit.WebView
import android.webkit.WebViewClient
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.withTimeoutOrNull
import org.json.JSONArray
import org.json.JSONObject
import org.json.JSONTokener

object YouTubeSession {
    private const val PREFS = "shell_youtube"
    private const val MAX_SENT = 1000
    const val HOME = "https://www.youtube.com/"
    const val HISTORY = "https://www.youtube.com/feed/history"
    const val SIGN_IN = "https://accounts.google.com/ServiceLogin?service=youtube&continue=https%3A%2F%2Fwww.youtube.com%2Fsignin%3Faction_handle_signin%3Dtrue%26next%3D%252F"

    private val extract = """
(function(){
var out=[];
var seen={};
function add(id){
if(typeof id==="string"&&/^[A-Za-z0-9_-]{11}${'$'}/.test(id)&&!seen[id]){seen[id]=1;out.push(id)}
}
function walk(o,depth){
if(!o||typeof o!=="object"||depth>40)return;
if(Array.isArray(o)){for(var i=0;i<o.length;i++)walk(o[i],depth+1);return}
if(o.videoRenderer&&o.videoRenderer.videoId)add(o.videoRenderer.videoId);
var l=o.lockupViewModel;
if(l&&l.contentType==="LOCKUP_CONTENT_TYPE_VIDEO"&&l.contentId)add(l.contentId);
for(var k in o)walk(o[k],depth+1)
}
walk(window.ytInitialData,0);
return JSON.stringify(out)
})()
""".trimIndent()

    fun signedIn(): Boolean = CookieManager.getInstance().getCookie(HOME)?.contains("SAPISID=") == true

    fun connected(context: Context): Boolean = prefs(context).getBoolean("connected", false) && signedIn()

    fun setConnected(context: Context, value: Boolean) {
        prefs(context).edit().putBoolean("connected", value).apply()
    }

    private fun prefs(context: Context) = context.applicationContext.getSharedPreferences(PREFS, Context.MODE_PRIVATE)

    fun sent(context: Context): List<String> =
        prefs(context).getString("sent", "")!!.split('\n').filter { it.isNotEmpty() }

    fun markSent(context: Context, ids: List<String>) {
        val merged = (sent(context) + ids).distinct().takeLast(MAX_SENT)
        prefs(context).edit().putString("sent", merged.joinToString("\n")).apply()
    }

    @SuppressLint("SetJavaScriptEnabled")
    suspend fun collect(context: Context): List<String> {
        val done = CompletableDeferred<List<String>>()
        val view = WebView(context.applicationContext)
        view.settings.javaScriptEnabled = true
        view.settings.domStorageEnabled = true
        view.settings.userAgentString = signInUserAgent(view.settings.userAgentString)
        view.webViewClient = object : WebViewClient() {
            override fun onPageFinished(v: WebView, url: String?) {
                val host = android.net.Uri.parse(url ?: "").host ?: ""
                if (!host.endsWith("youtube.com")) {
                    done.completeExceptionally(PluginException("not_signed_in"))
                    return
                }
                v.evaluateJavascript(extract) { raw ->
                    try {
                        val text = JSONTokener(raw).nextValue() as? String ?: "[]"
                        val array = JSONArray(text)
                        done.complete((0 until array.length()).map { array.getString(it) })
                    } catch (e: Exception) {
                        android.util.Log.e("Shell", "history extraction failed", e)
                        done.completeExceptionally(PluginException("extract_failed"))
                    }
                }
            }
        }
        view.loadUrl(HISTORY)
        try {
            return withTimeoutOrNull(30_000) { done.await() } ?: throw PluginException("timeout")
        } finally {
            view.destroy()
        }
    }

    fun signInUserAgent(default: String): String =
        default.replace("; wv", "").replace(Regex("Version/\\d+\\.\\d+ "), "")
}

class YouTubeHistoryPlugin : ShellPlugin {
    override val id = "youtube"

    override suspend fun handle(host: PluginHost, action: String, args: JSONObject): JSONObject {
        val ctx = host.activity
        return when (action) {
            "status" -> JSONObject().put("connected", YouTubeSession.connected(ctx))
            "signin" -> {
                val done = CompletableDeferred<Boolean>()
                YouTubeSignInActivity.pending = done
                ctx.runOnUiThread { ctx.startActivity(Intent(ctx, YouTubeSignInActivity::class.java)) }
                if (!done.await()) throw PluginException("cancelled")
                YouTubeSession.setConnected(ctx, true)
                JSONObject().put("connected", true)
            }
            "collect" -> {
                if (!YouTubeSession.connected(ctx)) throw PluginException("not_signed_in")
                val already = YouTubeSession.sent(ctx).toSet()
                val fresh = try {
                    YouTubeSession.collect(ctx).filter { it !in already }.take(200)
                } catch (e: PluginException) {
                    if (e.code == "not_signed_in") YouTubeSession.setConnected(ctx, false)
                    throw e
                }
                JSONObject().put("ids", JSONArray(fresh))
            }
            "ack" -> {
                val ids = args.optJSONArray("ids") ?: JSONArray()
                YouTubeSession.markSent(ctx, (0 until ids.length()).map { ids.getString(it) })
                JSONObject()
            }
            else -> throw PluginException("unknown_action")
        }
    }
}
