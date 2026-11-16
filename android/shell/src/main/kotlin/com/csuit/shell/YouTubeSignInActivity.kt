package com.csuit.shell

import android.annotation.SuppressLint
import android.graphics.Color
import android.net.Uri
import android.os.Bundle
import android.view.ViewGroup
import android.webkit.CookieManager
import android.webkit.WebView
import android.webkit.WebViewClient
import androidx.appcompat.app.AppCompatActivity
import kotlinx.coroutines.CompletableDeferred

class YouTubeSignInActivity : AppCompatActivity() {
    private var webView: WebView? = null
    private var finished = false

    @SuppressLint("SetJavaScriptEnabled")
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setTitle(R.string.shell_signin_title)
        val view = WebView(this)
        view.setBackgroundColor(Color.WHITE)
        view.layoutParams = ViewGroup.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT)
        view.settings.javaScriptEnabled = true
        view.settings.domStorageEnabled = true
        view.settings.userAgentString = YouTubeSession.signInUserAgent(view.settings.userAgentString)
        CookieManager.getInstance().setAcceptCookie(true)
        CookieManager.getInstance().setAcceptThirdPartyCookies(view, true)
        view.webViewClient = object : WebViewClient() {
            override fun onPageFinished(v: WebView, url: String?) {
                CookieManager.getInstance().flush()
                val host = Uri.parse(url ?: "").host ?: return
                if (host.endsWith("youtube.com") && YouTubeSession.signedIn()) finish(true)
            }
        }
        setContentView(view)
        webView = view
        view.loadUrl(YouTubeSession.SIGN_IN)
    }

    private fun finish(success: Boolean) {
        if (finished) return
        finished = true
        pending?.complete(success)
        pending = null
        finish()
    }

    override fun onDestroy() {
        if (!finished) {
            finished = true
            pending?.complete(false)
            pending = null
        }
        webView?.destroy()
        webView = null
        super.onDestroy()
    }

    companion object {
        var pending: CompletableDeferred<Boolean>? = null
    }
}
