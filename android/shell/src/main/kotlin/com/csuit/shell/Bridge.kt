package com.csuit.shell

import android.net.Uri
import android.webkit.WebView
import androidx.lifecycle.lifecycleScope
import androidx.webkit.JavaScriptReplyProxy
import androidx.webkit.WebMessageCompat
import androidx.webkit.WebViewCompat
import androidx.webkit.WebViewFeature
import kotlinx.coroutines.launch
import org.json.JSONObject

object Bridge {
    private const val PORT = "NativeShellPort"

    private val shim = """
(function(){
var port=window.$PORT;
if(!port)return;
var pending={};
var n=0;
port.onmessage=function(e){
var m;
try{m=JSON.parse(e.data)}catch(x){return}
var p=pending[m.id];
if(!p)return;
delete pending[m.id];
p(m)
};
window.NativeShell={call:function(plugin,action,args){
return new Promise(function(resolve){
var id=++n;
pending[id]=resolve;
port.postMessage(JSON.stringify({id:id,plugin:plugin,action:action,args:args||{}}))
})
}}
})();
""".trimIndent()

    fun supported(): Boolean =
        WebViewFeature.isFeatureSupported(WebViewFeature.WEB_MESSAGE_LISTENER) &&
            WebViewFeature.isFeatureSupported(WebViewFeature.DOCUMENT_START_SCRIPT)

    fun install(activity: MainActivity, webView: WebView, origin: String, plugins: List<ShellPlugin>): Boolean {
        if (!supported()) return false
        val allowed = setOf(origin)
        val byId = plugins.associateBy { it.id }
        val owner = activity
        val host = object : PluginHost {
            override val activity: MainActivity = owner
        }
        WebViewCompat.addDocumentStartJavaScript(webView, shim, allowed)
        WebViewCompat.addWebMessageListener(webView, PORT, allowed) { _, message: WebMessageCompat, sourceOrigin: Uri, isMainFrame: Boolean, reply: JavaScriptReplyProxy ->
            val raw = message.data
            if (!isMainFrame || raw == null || sourceOrigin.toString() != origin) return@addWebMessageListener
            activity.lifecycleScope.launch {
                reply.postMessage(dispatch(host, byId, raw))
            }
        }
        return true
    }

    private suspend fun dispatch(host: PluginHost, plugins: Map<String, ShellPlugin>, raw: String): String {
        val request = try {
            JSONObject(raw)
        } catch (e: Exception) {
            return JSONObject().put("id", 0).put("ok", false).put("error", "bad_request").toString()
        }
        val id = request.optInt("id", 0)
        val response = JSONObject().put("id", id)
        val plugin = plugins[request.optString("plugin")]
        if (plugin == null) {
            return response.put("ok", false).put("error", "unknown_plugin").toString()
        }
        return try {
            val result = plugin.handle(host, request.optString("action"), request.optJSONObject("args") ?: JSONObject())
            response.put("ok", true).put("result", result).toString()
        } catch (e: PluginException) {
            response.put("ok", false).put("error", e.code).toString()
        } catch (e: Exception) {
            android.util.Log.e("Shell", "plugin ${plugin.id} failed", e)
            response.put("ok", false).put("error", "failed").toString()
        }
    }
}
