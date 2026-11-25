package com.csuit.shell

import android.graphics.Color
import android.graphics.Typeface
import android.graphics.drawable.GradientDrawable
import android.os.Handler
import android.os.Looper
import android.util.Log
import android.util.TypedValue
import android.view.Gravity
import android.view.View
import android.view.ViewGroup
import android.webkit.WebView
import android.widget.FrameLayout
import android.widget.ImageView
import android.widget.LinearLayout
import android.widget.ScrollView
import android.widget.TextView
import android.widget.Toast
import androidx.appcompat.app.AppCompatDialog
import androidx.appcompat.app.AppCompatActivity
import androidx.core.view.GravityCompat
import androidx.core.view.WindowCompat
import androidx.core.view.ViewCompat
import androidx.core.view.WindowInsetsCompat
import androidx.drawerlayout.widget.DrawerLayout
import org.json.JSONObject
import org.json.JSONTokener

class NativeNav(
    private val activity: AppCompatActivity,
    private val webView: WebView,
    private val origin: String,
    private val bottomPaths: List<String>,
    private val onChangeInstance: () -> Unit,
) {
    private class Link(val label: String, val href: String, val icon: String, val active: Boolean)
    private class Entry(val heading: String?, val link: Link?)
    private class AcctItem(val label: String, val icon: String, val index: Int)
    private class State(
        val sidebarBg: Int,
        val sidebarText: Int,
        val accent: Int,
        val barBg: Int,
        val barText: Int,
        val pageBg: Int,
        val title: String,
        val menuLabel: String,
        val accountLabel: String,
        val dark: Boolean?,
        val entries: List<Entry>,
        val accountName: String,
        val accountItems: List<AcctItem>,
        val bellLabel: String,
        val hasBell: Boolean,
        val unread: String,
    )

    private val handler = Handler(Looper.getMainLooper())
    private val density = activity.resources.displayMetrics.density
    private var hasNav = false
    private var lastState: State? = null
    private val prefs = activity.getSharedPreferences(PREFS, android.content.Context.MODE_PRIVATE)
    private var inset = androidx.core.graphics.Insets.NONE

    val root = DrawerLayout(activity)
    val loading = LoadingOverlay(activity)
    private val holder = FrameLayout(activity)
    private val content = LinearLayout(activity)
    private val bottomBar = LinearLayout(activity)
    private val changeLink = TextView(activity)
    private val leftPanel = ScrollView(activity)
    private val rightPanel = ScrollView(activity)

    init {
        val savedBg = prefs.getInt(KEY_BG, 0)
        if (savedBg != 0) {
            root.setBackgroundColor(savedBg)
            content.setBackgroundColor(savedBg)
            webView.setBackgroundColor(savedBg)
        }
        content.orientation = LinearLayout.VERTICAL
        holder.addView(webView, FrameLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT))
        holder.addView(loading, FrameLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT))
        content.addView(holder, LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, 0, 1f))
        bottomBar.orientation = LinearLayout.HORIZONTAL
        bottomBar.visibility = View.GONE
        content.addView(bottomBar, LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT))
        changeLink.text = activity.getString(R.string.shell_change_instance)
        changeLink.setTextSize(TypedValue.COMPLEX_UNIT_SP, 13f)
        changeLink.setTextColor(0xFF8A94A6.toInt())
        changeLink.gravity = Gravity.CENTER
        changeLink.setPadding(dp(16), dp(10), dp(16), dp(10))
        changeLink.visibility = View.GONE
        changeLink.setOnClickListener { onChangeInstance() }
        content.addView(changeLink, LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT))
        root.addView(content, DrawerLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT))
        root.addView(leftPanel, DrawerLayout.LayoutParams(dp(300), ViewGroup.LayoutParams.MATCH_PARENT).apply { gravity = GravityCompat.START })
        root.addView(rightPanel, DrawerLayout.LayoutParams(dp(300), ViewGroup.LayoutParams.MATCH_PARENT).apply { gravity = GravityCompat.END })
        lock(true)
        WindowCompat.setDecorFitsSystemWindows(activity.window, false)
        ViewCompat.setOnApplyWindowInsetsListener(root) { _, insets ->
            val bars = insets.getInsets(WindowInsetsCompat.Type.systemBars() or WindowInsetsCompat.Type.displayCutout())
            inset = bars
            content.setPadding(bars.left, bars.top, bars.right, 0)
            bottomBar.setPadding(0, 0, 0, bars.bottom)
            changeLink.setPadding(dp(16), dp(10), dp(16), dp(10) + bars.bottom)
            leftPanel.setPadding(bars.left, bars.top, 0, bars.bottom)
            rightPanel.setPadding(0, bars.top, bars.right, bars.bottom)
            WindowInsetsCompat.CONSUMED
        }
    }

    fun closeDrawers(): Boolean {
        var closed = false
        if (root.isDrawerOpen(GravityCompat.START)) {
            root.closeDrawer(GravityCompat.START)
            closed = true
        }
        if (root.isDrawerOpen(GravityCompat.END)) {
            root.closeDrawer(GravityCompat.END)
            closed = true
        }
        return closed
    }

    fun onCommitVisible() {
        if (hasNav) webView.evaluateJavascript(HIDE_JS, null)
    }

    fun onPageFinished() {
        webView.evaluateJavascript(SCRAPE_JS) { raw ->
            handler.post { apply(raw) }
        }
    }

    private fun apply(raw: String?) {
        val json = unwrap(raw)
        if (json == null) {
            Log.w(TAG, "navigation scrape returned nothing, showing the web navigation")
            setNav(false)
            return
        }
        val state = try {
            parse(json)
        } catch (e: Exception) {
            Log.e(TAG, "navigation scrape could not be parsed, showing the web navigation", e)
            setNav(false)
            return
        }
        val hasLinks = state.entries.any { it.link != null }
        if (!hasLinks && state.accountItems.isEmpty()) {
            setNav(false)
            return
        }
        setNav(true)
        webView.evaluateJavascript(HIDE_JS, null)
        build(state)
    }

    private fun setNav(on: Boolean) {
        hasNav = on
        if (!on) {
            prefs.edit().putBoolean(KEY_NAV_OK, false).apply()
            webView.evaluateJavascript(UNHIDE_JS, null)
        }
        bottomBar.visibility = if (on) View.VISIBLE else View.GONE
        changeLink.visibility = if (on) View.GONE else View.VISIBLE
        lock(!on)
        if (!on) closeDrawers()
    }

    private fun lock(locked: Boolean) {
        val mode = if (locked) DrawerLayout.LOCK_MODE_LOCKED_CLOSED else DrawerLayout.LOCK_MODE_UNLOCKED
        root.setDrawerLockMode(mode, GravityCompat.START)
        root.setDrawerLockMode(mode, GravityCompat.END)
    }

    private fun unwrap(raw: String?): String? {
        if (raw.isNullOrEmpty() || raw == "null") return null
        val value = JSONTokener(raw).nextValue()
        val text = value as? String ?: return null
        return text.ifEmpty { null }
    }

    private fun parse(text: String): State {
        val o = JSONObject(text)
        val c = o.optJSONObject("colors") ?: JSONObject()
        val items = o.optJSONArray("items")
        val entries = mutableListOf<Entry>()
        if (items != null) {
            for (i in 0 until items.length()) {
                val it = items.getJSONObject(i)
                if (it.optString("t") == "h") {
                    entries.add(Entry(it.optString("l"), null))
                } else {
                    entries.add(Entry(null, Link(it.optString("l"), it.optString("h"), it.optString("i"), it.optBoolean("a"))))
                }
            }
        }
        val acct = o.optJSONObject("acct")
        val acctItems = mutableListOf<AcctItem>()
        val acctArr = acct?.optJSONArray("items")
        if (acctArr != null) {
            for (i in 0 until acctArr.length()) {
                val it = acctArr.getJSONObject(i)
                acctItems.add(AcctItem(it.optString("l"), it.optString("i"), it.optInt("x")))
            }
        }
        val bell = o.optJSONObject("bell")
        return State(
            sidebarBg = color(c.optString("sidebarBg"), COLOR_SIDEBAR_BG),
            sidebarText = color(c.optString("sidebarText"), COLOR_SIDEBAR_TEXT),
            accent = color(c.optString("accent"), COLOR_ACCENT),
            barBg = color(c.optString("bg"), COLOR_SIDEBAR_BG),
            barText = color(c.optString("text"), COLOR_SIDEBAR_TEXT),
            pageBg = color(c.optString("bg"), COLOR_SIDEBAR_BG),
            title = o.optString("title"),
            menuLabel = o.optString("menuLabel"),
            accountLabel = acct?.optString("label").orEmpty(),
            dark = if (o.has("dark")) o.optBoolean("dark") else null,
            entries = entries,
            accountName = acct?.optString("name").orEmpty(),
            accountItems = acctItems,
            bellLabel = bell?.optString("label").orEmpty(),
            hasBell = bell != null,
            unread = bell?.optString("unread").orEmpty(),
        )
    }

    private fun color(css: String, fallback: Int): Int {
        val m = RGB.find(css) ?: return fallback
        return Color.rgb(m.groupValues[1].toInt(), m.groupValues[2].toInt(), m.groupValues[3].toInt())
    }

    private fun build(s: State) {
        lastState = s
        prefs.edit().putInt(KEY_BG, s.pageBg).putBoolean(KEY_NAV_OK, true).apply()
        webView.setBackgroundColor(s.pageBg)
        root.setBackgroundColor(s.pageBg)
        content.setBackgroundColor(s.pageBg)
        WindowCompat.getInsetsController(activity.window, root).isAppearanceLightStatusBars = Color.luminance(s.pageBg) > 0.5f
        WindowCompat.getInsetsController(activity.window, root).isAppearanceLightNavigationBars = Color.luminance(s.pageBg) > 0.5f
        buildBottomBar(s)
        buildLeft(s)
        buildRight(s)
    }

    private fun buildBottomBar(s: State) {
        bottomBar.removeAllViews()
        bottomBar.setBackgroundColor(s.barBg)
        bottomBar.elevation = dp(8).toFloat()
        bottomBar.dividerDrawable = null
        val links = s.entries.mapNotNull { it.link }
        val picked = pins().mapNotNull { p -> links.firstOrNull { pathOf(it.href) == p } }.take(MAX_PINS)
        val destinations = if (picked.isNotEmpty()) picked else links.take(3)
        if (links.isNotEmpty()) {
            bottomBar.addView(barButton(s, "fa-menu", s.menuLabel, false) { root.openDrawer(GravityCompat.START) })
        }
        for (l in destinations) {
            bottomBar.addView(barButton(s, l.icon, l.label, l.active) { go(l.href) })
        }
        if (s.accountItems.isNotEmpty() || s.hasBell) {
            bottomBar.addView(barButton(s, "fa-user", s.accountLabel, false) {
                refreshUnread()
                root.openDrawer(GravityCompat.END)
            })
        }
    }

    private fun barButton(s: State, icon: String, label: String, active: Boolean, onClick: () -> Unit): View {
        val tint = if (active) s.accent else s.barText
        val box = LinearLayout(activity)
        box.orientation = LinearLayout.VERTICAL
        box.gravity = Gravity.CENTER
        box.setPadding(dp(4), dp(8), dp(4), dp(8))
        box.layoutParams = LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f)
        box.addView(iconView(icon, tint, 24))
        val t = TextView(activity)
        t.text = label
        t.setTextColor(tint)
        t.setTextSize(TypedValue.COMPLEX_UNIT_SP, 11f)
        t.maxLines = 1
        t.gravity = Gravity.CENTER
        t.ellipsize = android.text.TextUtils.TruncateAt.END
        box.addView(t)
        box.setOnClickListener { onClick() }
        return box
    }

    private fun buildLeft(s: State) {
        val col = LinearLayout(activity)
        col.orientation = LinearLayout.VERTICAL
        col.setPadding(dp(14), dp(18), dp(14), dp(18))
        leftPanel.removeAllViews()
        leftPanel.setBackgroundColor(s.sidebarBg)
        leftPanel.addView(col)

        val header = LinearLayout(activity)
        header.orientation = LinearLayout.HORIZONTAL
        header.gravity = Gravity.CENTER_VERTICAL
        header.setPadding(dp(14), 0, dp(14), dp(14))
        val title = TextView(activity)
        title.text = s.title
        title.setTextColor(s.sidebarText)
        title.setTextSize(TypedValue.COMPLEX_UNIT_SP, 22f)
        title.setTypeface(null, Typeface.BOLD)
        header.addView(title, LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f))
        if (s.dark != null) {
            header.addView(darkSwitch(s.dark))
        }
        col.addView(header)

        for (e in s.entries) {
            if (e.heading != null) {
                val h = TextView(activity)
                h.text = e.heading
                h.setTextColor(withAlpha(s.sidebarText, 150))
                h.setTextSize(TypedValue.COMPLEX_UNIT_SP, 12f)
                h.setTypeface(null, Typeface.BOLD)
                h.setPadding(dp(14), dp(16), dp(14), dp(6))
                col.addView(h)
            } else if (e.link != null) {
                val l = e.link
                col.addView(row(s, l.icon, l.label, l.active) { go(l.href) })
            }
        }
    }

    private fun darkSwitch(checked: Boolean): View {
        var on = checked
        val frame = FrameLayout(activity)
        val track = GradientDrawable()
        track.cornerRadius = dp(40).toFloat()
        track.setColor(0xFF334155.toInt())
        frame.background = track
        val moon = iconView("fa-moon", 0xFFF1C40F.toInt(), 17)
        val sun = iconView("fa-sun", 0xFFF39C12.toInt(), 17)
        frame.addView(moon, FrameLayout.LayoutParams(dp(17), dp(17), Gravity.START or Gravity.CENTER_VERTICAL).apply { marginStart = dp(11) })
        frame.addView(sun, FrameLayout.LayoutParams(dp(17), dp(17), Gravity.END or Gravity.CENTER_VERTICAL).apply { marginEnd = dp(11) })
        val ball = View(activity)
        val disc = GradientDrawable()
        disc.shape = GradientDrawable.OVAL
        disc.setColor(Color.WHITE)
        ball.background = disc
        ball.elevation = dp(2).toFloat()
        ball.translationX = if (on) dp(25).toFloat() else 0f
        frame.addView(ball, FrameLayout.LayoutParams(dp(30), dp(30), Gravity.START or Gravity.CENTER_VERTICAL).apply { marginStart = dp(5) })
        frame.setOnClickListener {
            on = !on
            ball.animate().translationX(if (on) dp(25).toFloat() else 0f).setDuration(250).start()
            toggleDark()
        }
        frame.layoutParams = LinearLayout.LayoutParams(dp(65), dp(40))
        return frame
    }

    private fun buildRight(s: State) {
        val col = LinearLayout(activity)
        col.orientation = LinearLayout.VERTICAL
        col.setPadding(dp(14), dp(18), dp(14), dp(18))
        rightPanel.removeAllViews()
        rightPanel.isFillViewport = true
        rightPanel.setBackgroundColor(s.sidebarBg)
        rightPanel.addView(col)

        val header = LinearLayout(activity)
        header.orientation = LinearLayout.HORIZONTAL
        header.gravity = Gravity.CENTER_VERTICAL
        header.setPadding(dp(14), 0, dp(14), dp(14))
        val initial = TextView(activity)
        initial.text = s.accountName.trim().take(1).uppercase()
        initial.gravity = Gravity.CENTER
        initial.setTextColor(Color.WHITE)
        initial.setTypeface(null, Typeface.BOLD)
        val disc = GradientDrawable()
        disc.shape = GradientDrawable.OVAL
        disc.setColor(s.accent)
        initial.background = disc
        header.addView(initial, LinearLayout.LayoutParams(dp(40), dp(40)))
        val name = TextView(activity)
        name.text = s.accountName
        name.setTextColor(s.sidebarText)
        name.setTextSize(TypedValue.COMPLEX_UNIT_SP, 16f)
        name.setPadding(dp(12), 0, 0, 0)
        header.addView(name, LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f))
        col.addView(header)

        if (s.hasBell) {
            val label = if (s.unread.isNotEmpty()) "${s.bellLabel} (${s.unread})" else s.bellLabel
            col.addView(row(s, "fa-bell", label, false) { openBell() })
        }
        for (a in s.accountItems) {
            col.addView(row(s, a.icon, a.label, false) { clickAccount(a.index) })
        }
        val spacer = View(activity)
        col.addView(spacer, LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, 0, 1f))
        val rule = View(activity)
        rule.setBackgroundColor(withAlpha(s.sidebarText, 40))
        col.addView(rule, LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, dp(1)).apply {
            topMargin = dp(24)
            bottomMargin = dp(8)
        })
        col.addView(row(s, "fa-gear", activity.getString(R.string.shell_app_settings), false) { openSettings() })
    }

    private fun pins(): List<String> {
        val saved = prefs.getString(KEY_PINS, null)
        if (saved == null) return bottomPaths
        return saved.split('\n').filter { it.isNotEmpty() }
    }

    private fun savePins(list: List<String>) {
        prefs.edit().putString(KEY_PINS, list.joinToString("\n")).apply()
        lastState?.let { buildBottomBar(it) }
    }

    private fun openSettings() {
        val s = lastState
        root.closeDrawers()
        if (s == null) {
            Log.e(TAG, "settings opened before the navigation was scraped")
            Toast.makeText(activity, R.string.shell_settings_unavailable, Toast.LENGTH_SHORT).show()
            return
        }
        val dialog = AppCompatDialog(activity, R.style.Theme_Shell)
        val scroll = ScrollView(activity)
        scroll.setBackgroundColor(s.sidebarBg)
        scroll.setPadding(inset.left, inset.top, inset.right, inset.bottom)
        val col = LinearLayout(activity)
        col.orientation = LinearLayout.VERTICAL
        col.setPadding(dp(14), dp(18), dp(14), dp(18))
        scroll.addView(col)

        val header = LinearLayout(activity)
        header.orientation = LinearLayout.HORIZONTAL
        header.gravity = Gravity.CENTER_VERTICAL
        header.setPadding(dp(14), 0, dp(14), dp(8))
        val title = TextView(activity)
        title.text = activity.getString(R.string.shell_app_settings)
        title.setTextColor(s.sidebarText)
        title.setTextSize(TypedValue.COMPLEX_UNIT_SP, 22f)
        title.setTypeface(null, Typeface.BOLD)
        header.addView(title, LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f))
        val close = iconView("fa-x", s.sidebarText, 22)
        close.setPadding(dp(8), dp(8), dp(8), dp(8))
        close.layoutParams = LinearLayout.LayoutParams(dp(38), dp(38))
        close.contentDescription = activity.getString(R.string.shell_close)
        close.setOnClickListener { dialog.dismiss() }
        header.addView(close)
        col.addView(header)

        col.addView(sectionLabel(s, activity.getString(R.string.shell_settings_pins)))
        val hint = TextView(activity)
        hint.text = activity.getString(R.string.shell_settings_pins_hint, MAX_PINS)
        hint.setTextColor(withAlpha(s.sidebarText, 170))
        hint.setTextSize(TypedValue.COMPLEX_UNIT_SP, 13f)
        hint.setPadding(dp(14), 0, dp(14), dp(8))
        col.addView(hint)

        val links = s.entries.mapNotNull { it.link }.filter { pathOf(it.href).isNotEmpty() }
        val pinned = pins().toMutableList()
        for (l in links) {
            val path = pathOf(l.href)
            val mark = iconView("fa-check", s.accent, 20)
            mark.visibility = if (pinned.contains(path)) View.VISIBLE else View.INVISIBLE
            val r = row(s, l.icon, l.label, false) {}
            r.addView(View(activity), LinearLayout.LayoutParams(0, 1, 1f))
            r.addView(mark)
            r.setOnClickListener {
                if (pinned.contains(path)) {
                    pinned.remove(path)
                    mark.visibility = View.INVISIBLE
                } else if (pinned.size >= MAX_PINS) {
                    Toast.makeText(activity, activity.getString(R.string.shell_settings_pins_max, MAX_PINS), Toast.LENGTH_SHORT).show()
                    return@setOnClickListener
                } else {
                    pinned.add(path)
                    mark.visibility = View.VISIBLE
                }
                savePins(pinned.toList())
            }
            col.addView(r)
        }

        col.addView(sectionLabel(s, activity.getString(R.string.shell_settings_account)))
        col.addView(row(s, "fa-store", activity.getString(R.string.shell_change_business), false) {
            dialog.dismiss()
            onChangeInstance()
        })

        dialog.setContentView(scroll)
        dialog.window?.setLayout(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT)
        dialog.show()
    }

    private fun sectionLabel(s: State, text: String): View {
        val h = TextView(activity)
        h.text = text
        h.setTextColor(withAlpha(s.sidebarText, 150))
        h.setTextSize(TypedValue.COMPLEX_UNIT_SP, 12f)
        h.setTypeface(null, Typeface.BOLD)
        h.setPadding(dp(14), dp(16), dp(14), dp(6))
        return h
    }

    private fun row(s: State, icon: String, label: String, active: Boolean, onClick: () -> Unit): LinearLayout {
        val tint = if (active) s.accent else s.sidebarText
        val r = LinearLayout(activity)
        r.orientation = LinearLayout.HORIZONTAL
        r.gravity = Gravity.CENTER_VERTICAL
        r.setPadding(dp(14), dp(12), dp(14), dp(12))
        if (active) {
            val bg = GradientDrawable()
            bg.cornerRadius = dp(8).toFloat()
            bg.setColor(withAlpha(s.accent, 40))
            r.background = bg
        }
        r.addView(iconView(icon, tint, 22))
        val t = TextView(activity)
        t.text = label
        t.setTextColor(tint)
        t.setTextSize(TypedValue.COMPLEX_UNIT_SP, 15f)
        t.setPadding(dp(14), 0, 0, 0)
        r.addView(t)
        r.setOnClickListener { onClick() }
        r.layoutParams = LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT).apply {
            topMargin = dp(2)
            bottomMargin = dp(2)
        }
        return r
    }

    private fun iconView(icon: String, tint: Int, sizeDp: Int): ImageView {
        val v = ImageView(activity)
        val res = iconRes(icon)
        if (res != 0) {
            v.setImageResource(res)
            v.setColorFilter(tint)
        } else if (icon.isNotEmpty()) {
            Log.w(TAG, "no native icon for '$icon'")
        }
        v.layoutParams = LinearLayout.LayoutParams(dp(sizeDp), dp(sizeDp))
        return v
    }

    private fun iconRes(icon: String): Int {
        if (icon.isEmpty()) return 0
        val name = "shell_icon_" + icon.lowercase().replace(Regex("[^a-z0-9]"), "_")
        return activity.resources.getIdentifier(name, "drawable", activity.packageName)
    }

    private fun go(href: String) {
        root.closeDrawers()
        if (!href.startsWith("/") || href.startsWith("//") || href.contains("\\")) {
            Log.w(TAG, "refusing navigation to non-local path")
            return
        }
        webView.loadUrl(origin + href)
    }

    private fun clickAccount(index: Int) {
        root.closeDrawers()
        webView.evaluateJavascript(
            "(function(i){var e=document.querySelectorAll('.account-dropdown-menu .account-dropdown-item')[i];if(e){e.click();return 1;}return 0;})($index)",
        ) { result ->
            if (result != "1") Log.e(TAG, "account menu item $index not found on the page")
        }
    }

    private fun openBell() {
        root.closeDrawers()
        webView.evaluateJavascript(
            "(function(){var b=document.querySelector('.fixed-auth-area .nk-button');if(b){b.click();return 1;}return 0;})()",
        ) { result ->
            if (result != "1") Log.e(TAG, "notification bell not found on the page")
        }
    }

    private fun toggleDark() {
        webView.evaluateJavascript("(function(){var e=document.getElementById('dswitch');if(e){e.click();return 1;}return 0;})()") { result ->
            if (result != "1") Log.e(TAG, "dark mode switch not found on the page")
            handler.postDelayed({ onPageFinished() }, 400)
        }
    }

    private fun refreshUnread() {
        if (hasNav) onPageFinished()
    }

    private fun pathOf(href: String): String = href.substringBefore('?').substringBefore('#')

    private fun withAlpha(c: Int, a: Int): Int = Color.argb(a, Color.red(c), Color.green(c), Color.blue(c))

    private fun dp(v: Int): Int = (v * density).toInt()

    companion object {
        private const val TAG = "ShellNav"
        const val PREFS = "shell_nav"
        const val KEY_BG = "bg"
        const val KEY_NAV_OK = "nav_ok"
        private const val KEY_PINS = "pins"
        private const val MAX_PINS = 4
        private val RGB = Regex("""rgba?\((\d+),\s*(\d+),\s*(\d+)""")
        private val COLOR_SIDEBAR_BG = Color.parseColor("#1B1E27")
        private val COLOR_SIDEBAR_TEXT = Color.parseColor("#F2F3F7")
        private val COLOR_ACCENT = Color.parseColor("#7C5CFF")

        const val UNHIDE_JS =
            "(function(){try{var s=window.__shellNavSheet;if(s){document.adoptedStyleSheets=document.adoptedStyleSheets.filter(function(x){return x!==s;});}" +
                "window.__shellNavHide=0;window.__shellNavSheet=null;}catch(e){}})()"

        const val HIDE_JS =
            "(function(){try{if(window.__shellNavHide)return;window.__shellNavHide=1;var s=new CSSStyleSheet();window.__shellNavSheet=s;" +
                "s.replaceSync('.admin-sidebar,.admin-topbar-nav-toggle,.fixed-auth-area .nk-button,#dark-switch,.account-dropdown,.admin-content h1{display:none!important}" +
                ".admin-content{margin-inline-start:0!important;width:100%!important;max-width:100%!important;padding:8px!important;min-height:0!important;box-sizing:border-box!important}" +
                ".admin-shell-offset{padding-top:0!important}');" +
                "document.adoptedStyleSheets=document.adoptedStyleSheets.concat([s]);}catch(e){}})()"

        private const val SCRAPE_JS =
            "(function(){" +
                "function cv(n){var v=getComputedStyle(document.body).getPropertyValue(n).trim();if(!v)return '';" +
                "var e=document.createElement('span');e.style.color=v;document.body.appendChild(e);var c=getComputedStyle(e).color;e.remove();return c;}" +
                "function pc(p){var m=document.querySelector('.admin-content')||document.body;return getComputedStyle(m)[p===\'color\'?\'color\':\'backgroundColor\'];}" +
                "function ic(root){var u=root.querySelector('use');var h=u?u.getAttribute('href'):'';return h&&h.indexOf('#')>=0?h.split('#')[1]:'';}" +
                "var side=document.querySelector('#admin-sidebar .sidebar-nav')||document.querySelector('#admin-sidebar nav');" +
                "var drop=document.querySelector('.account-dropdown-menu');" +
                "var bell=document.querySelector('.fixed-auth-area .nk-root');" +
                "if(!side&&!drop&&!bell)return '';" +
                "var items=[];" +
                "if(side){side.querySelectorAll('.sidebar-nav-heading,.sidebar-group-summary,a').forEach(function(n){" +
                "if(n.matches('.sidebar-nav-heading,.sidebar-group-summary')){var sp=n.querySelector('span');var ht=(sp?sp.textContent:n.textContent).trim();if(ht)items.push({t:'h',l:ht});return;}" +
                "var hr=n.getAttribute('href')||'';var lb=n.textContent.trim();if(!lb||hr.charAt(0)!=='/')return;" +
                "items.push({t:'a',l:lb,h:hr,i:ic(n),a:n.classList.contains('is-active')||n.classList.contains('active')||n.getAttribute('aria-current')==='page'});});}" +
                "var acct=null;if(drop){var ai=[];drop.querySelectorAll('.account-dropdown-item').forEach(function(e,i){" +
                "ai.push({l:e.textContent.trim(),i:ic(e),x:i});});" +
                "var nm=document.querySelector('.account-name');var bt=document.querySelector('.account-dropdown-button');" +
                "acct={name:nm?nm.textContent.trim():'',label:bt?(bt.getAttribute('aria-label')||''):'',items:ai};}" +
                "var bl=null;if(bell){var bb=bell.querySelector('[aria-label]');var bg=bell.querySelector('.nk-badge');" +
                "bl={label:bb?(bb.getAttribute('aria-label')||''):'',unread:bg&&!bg.hidden?bg.textContent.trim():''};}" +
                "var tg=document.querySelector('.admin-topbar-nav-toggle,[data-admin-sidebar-toggle]');var th=document.querySelector('#admin-sidebar .sidebar-header h2');" +
                "var ds=document.getElementById('dswitch');" +
                "var o={colors:{sidebarBg:cv('--sidebar-bg'),sidebarText:cv('--sidebar-text'),accent:cv('--primary-color'),bg:pc('background-color'),text:pc('color')}," +
                "title:th?th.textContent.trim():'',menuLabel:tg?(tg.getAttribute('aria-label')||''):'',items:items,acct:acct,bell:bl};" +
                "if(ds)o.dark=!!ds.checked;" +
                "return JSON.stringify(o);})()"
    }
}
