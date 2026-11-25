package com.csuit.shell

import android.Manifest
import android.annotation.SuppressLint
import android.content.ActivityNotFoundException
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.view.ViewGroup
import android.webkit.CookieManager
import android.webkit.ValueCallback
import android.webkit.WebChromeClient
import android.webkit.WebResourceError
import android.webkit.WebResourceRequest
import android.webkit.WebView
import android.webkit.WebViewClient
import android.widget.FrameLayout
import androidx.activity.OnBackPressedCallback
import androidx.activity.result.contract.ActivityResultContracts
import androidx.appcompat.app.AlertDialog
import androidx.appcompat.app.AppCompatActivity
import androidx.core.content.ContextCompat
import androidx.core.splashscreen.SplashScreen.Companion.installSplashScreen
import androidx.webkit.WebSettingsCompat
import androidx.webkit.WebViewCompat
import androidx.webkit.WebViewFeature

class MainActivity : AppCompatActivity() {
    private var webView: WebView? = null
    private var nav: NativeNav? = null
    private var origin: String = ""
    private var host: String = ""
    private var filePathCallback: ValueCallback<Array<Uri>>? = null
    private var errorShown = false
    private var overlay: LoadingOverlay? = null
    private var stopped = false

    private val fileChooser = registerForActivityResult(ActivityResultContracts.StartActivityForResult()) { result ->
        val data = if (result.resultCode == RESULT_OK) result.data else null
        filePathCallback?.onReceiveValue(WebChromeClient.FileChooserParams.parseResult(result.resultCode, data))
        filePathCallback = null
    }

    private val notificationPermission = registerForActivityResult(ActivityResultContracts.RequestPermission()) { }

    override fun onCreate(savedInstanceState: Bundle?) {
        installSplashScreen()
        super.onCreate(savedInstanceState)
        val subdomain = InstanceStore(this).subdomain()
        if (subdomain == null) {
            startActivity(Intent(this, InstanceActivity::class.java))
            finish()
            return
        }
        val config = Shell.config
        origin = config.baseUrl(subdomain)
        host = config.host(subdomain)
        setupWebView()
        onBackPressedDispatcher.addCallback(this, object : OnBackPressedCallback(true) {
            override fun handleOnBackPressed() {
                if (nav?.closeDrawers() == true) return
                val view = webView
                if (view != null && view.canGoBack()) {
                    view.goBack()
                } else {
                    finish()
                }
            }
        })
        val restored = savedInstanceState?.let { webView?.restoreState(it) }
        if (restored == null) webView?.loadUrl(startUrl(intent))
    }

    override fun onSaveInstanceState(outState: Bundle) {
        super.onSaveInstanceState(outState)
        webView?.saveState(outState)
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        setIntent(intent)
        webView?.loadUrl(startUrl(intent))
    }

    override fun onStop() {
        stopped = true
        super.onStop()
    }

    override fun onStart() {
        super.onStart()
        if (!stopped) return
        stopped = false
        val view = webView ?: return
        overlay?.show(0)
        view.postDelayed({ overlay?.hide() }, START_MAX_MS)
        view.onResume()
        view.resumeTimers()
        if (WebViewFeature.isFeatureSupported(WebViewFeature.VISUAL_STATE_CALLBACK)) {
            WebViewCompat.postVisualStateCallback(view, 1L, WebViewCompat.VisualStateCallback { overlay?.hide() })
        }
        view.postDelayed({ overlay?.hide() }, RESUME_MAX_MS)
    }

    override fun onDestroy() {
        webView?.let {
            (it.parent as? ViewGroup)?.removeView(it)
            it.destroy()
        }
        webView = null
        nav = null
        super.onDestroy()
    }

    private fun startUrl(intent: Intent?): String {
        val link = intent?.getStringExtra(EXTRA_LINK)
        if (link != null && link.startsWith("/") && !link.startsWith("//") && !link.contains("\\")) {
            return origin + link
        }
        return "$origin/"
    }

    @SuppressLint("SetJavaScriptEnabled")
    private fun setupWebView() {
        val view = WebView(this)
        if (Shell.config.nativeNav) {
            val created = NativeNav(this, view, origin, Shell.config.bottomNavPaths) { changeInstance() }
            nav = created
            overlay = created.loading
            setContentView(created.root)
        } else {
            val layout = FrameLayout(this)
            layout.addView(view, FrameLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT))
            val plain = LoadingOverlay(this)
            layout.addView(plain, FrameLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT))
            overlay = plain
            setContentView(layout)
        }
        webView = view

        if (WebViewFeature.isFeatureSupported(WebViewFeature.SAFE_BROWSING_ENABLE)) {
            WebSettingsCompat.setSafeBrowsingEnabled(view.settings, false)
        }
        if (nav != null && getSharedPreferences(NativeNav.PREFS, MODE_PRIVATE).getBoolean(NativeNav.KEY_NAV_OK, false) &&
            WebViewFeature.isFeatureSupported(WebViewFeature.DOCUMENT_START_SCRIPT)
        ) {
            WebViewCompat.addDocumentStartJavaScript(view, NativeNav.HIDE_JS, setOf(origin))
        }
        view.settings.apply {
            javaScriptEnabled = true
            domStorageEnabled = true
            allowFileAccess = false
            allowContentAccess = false
            setSupportMultipleWindows(false)
            userAgentString = userAgentString + " " + Shell.config.appName.filter { it.isLetterOrDigit() } + "Android/1"
        }
        if (WebViewFeature.isFeatureSupported(WebViewFeature.WEB_AUTHENTICATION)) {
            WebSettingsCompat.setWebAuthenticationSupport(view.settings, WebSettingsCompat.WEB_AUTHENTICATION_SUPPORT_FOR_APP)
        } else {
            android.util.Log.w("Shell", "WebView has no WebAuthn support; passkeys will not work in this app")
        }
        CookieManager.getInstance().apply {
            setAcceptCookie(true)
            setAcceptThirdPartyCookies(view, false)
        }

        view.webViewClient = object : WebViewClient() {
            override fun shouldOverrideUrlLoading(v: WebView, request: WebResourceRequest): Boolean {
                val uri = request.url
                if (uri.scheme == "https" && uri.host == host) return false
                openExternal(uri)
                return true
            }

            override fun onReceivedError(v: WebView, request: WebResourceRequest, error: WebResourceError) {
                if (request.isForMainFrame) {
                    overlay?.hide()
                    showLoadError()
                }
            }

            override fun onRenderProcessGone(v: WebView, detail: android.webkit.RenderProcessGoneDetail): Boolean {
                android.util.Log.e("Shell", "web renderer process gone (crashed=${detail.didCrash()}), restarting the screen")
                runOnUiThread { recreate() }
                return true
            }

            override fun onPageStarted(v: WebView, url: String?, favicon: android.graphics.Bitmap?) {
                overlay?.show(LOADING_DELAY_MS)
            }

            override fun onPageCommitVisible(v: WebView, url: String?) {
                overlay?.hide()
                nav?.onCommitVisible()
            }

            override fun onPageFinished(v: WebView, url: String?) {
                overlay?.hide()
                errorShown = false
                CookieManager.getInstance().flush()
                nav?.onPageFinished()
            }
        }

        view.webChromeClient = object : WebChromeClient() {
            override fun onShowFileChooser(v: WebView, callback: ValueCallback<Array<Uri>>, params: FileChooserParams): Boolean {
                filePathCallback?.onReceiveValue(null)
                filePathCallback = callback
                return try {
                    fileChooser.launch(params.createIntent())
                    true
                } catch (e: ActivityNotFoundException) {
                    android.util.Log.e("Shell", "no file picker available", e)
                    filePathCallback = null
                    callback.onReceiveValue(null)
                    false
                }
            }
        }

        if (!Bridge.install(this, view, origin, Shell.plugins())) {
            android.util.Log.w("Shell", "WebView does not support the message bridge; native features are disabled")
        }
    }

    private fun openExternal(uri: Uri) {
        val scheme = uri.scheme ?: return
        if (scheme != "https" && scheme != "http" && scheme != "tg" && scheme != "mailto") return
        try {
            startActivity(Intent(Intent.ACTION_VIEW, uri))
        } catch (e: ActivityNotFoundException) {
            android.util.Log.w("Shell", "no app to open $scheme link", e)
        }
    }

    private fun showLoadError() {
        if (errorShown || isFinishing) return
        errorShown = true
        AlertDialog.Builder(this)
            .setTitle(R.string.shell_load_error_title)
            .setMessage(R.string.shell_load_error_text)
            .setPositiveButton(R.string.shell_retry) { _, _ ->
                errorShown = false
                webView?.loadUrl(startUrl(intent))
            }
            .setNegativeButton(R.string.shell_change_instance) { _, _ -> changeInstance() }
            .setCancelable(false)
            .show()
    }

    fun changeInstance() {
        InstanceStore(this).clear()
        CookieManager.getInstance().removeAllCookies(null)
        startActivity(Intent(this, InstanceActivity::class.java))
        finish()
    }

    fun ensureNotificationPermission() {
        if (Build.VERSION.SDK_INT >= 33 &&
            ContextCompat.checkSelfPermission(this, Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED
        ) {
            notificationPermission.launch(Manifest.permission.POST_NOTIFICATIONS)
        }
    }

    companion object {
        const val EXTRA_LINK = "link"
        private const val LOADING_DELAY_MS = 250L
        private const val RESUME_MAX_MS = 2500L
        private const val START_MAX_MS = 20000L
    }
}
