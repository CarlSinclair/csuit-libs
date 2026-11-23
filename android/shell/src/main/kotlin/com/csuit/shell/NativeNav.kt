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
import android.widget.ImageView
import android.widget.LinearLayout
import android.widget.ScrollView
import android.widget.TextView
import androidx.appcompat.app.AppCompatActivity
import androidx.appcompat.widget.SwitchCompat
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
    private var inset = androidx.core.graphics.Insets.NONE

    val root = DrawerLayout(activity)
    private val content = LinearLayout(activity)
    private val bottomBar = LinearLayout(activity)
    private val leftPanel = ScrollView(activity)
    private val rightPanel = ScrollView(activity)

    init {
        content.orientation = LinearLayout.VERTICAL
        content.addView(webView, LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, 0, 1f))
        bottomBar.orientation = LinearLayout.HORIZONTAL
        bottomBar.visibility = View.GONE
        content.addView(bottomBar, LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT))
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
        bottomBar.visibility = if (on) View.VISIBLE else View.GONE
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
        val picked = bottomPaths.mapNotNull { p -> links.firstOrNull { pathOf(it.href) == p } }.take(3)
        val destinations = if (picked.size >= 2) picked else links.take(3)
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
            val toggle = SwitchCompat(activity)
            toggle.isChecked = s.dark
            toggle.setOnCheckedChangeListener { _, _ -> toggleDark() }
            header.addView(toggle)
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

        val change = row(s, "fa-gear", activity.getString(R.string.shell_change_instance), false) { onChangeInstance() }
        col.addView(change)
    }

    private fun buildRight(s: State) {
        val col = LinearLayout(activity)
        col.orientation = LinearLayout.VERTICAL
        col.setPadding(dp(14), dp(18), dp(14), dp(18))
        rightPanel.removeAllViews()
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
    }

    private fun row(s: State, icon: String, label: String, active: Boolean, onClick: () -> Unit): View {
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
        private val RGB = Regex("""rgba?\((\d+),\s*(\d+),\s*(\d+)""")
        private val COLOR_SIDEBAR_BG = Color.parseColor("#1B1E27")
        private val COLOR_SIDEBAR_TEXT = Color.parseColor("#F2F3F7")
        private val COLOR_ACCENT = Color.parseColor("#7C5CFF")

        private const val HIDE_JS =
            "(function(){try{if(window.__shellNavHide)return;window.__shellNavHide=1;var s=new CSSStyleSheet();" +
                "s.replaceSync('.admin-sidebar,.admin-topbar-nav-toggle,.fixed-auth-area .nk-button,#dark-switch,.account-dropdown,.admin-content h1{display:none!important}" +
                ".admin-content{margin-inline-start:0!important;width:100%!important;max-width:100%!important;padding:8px!important;min-height:0!important}" +
                ".admin-shell-offset{padding-top:0!important}');" +
                "document.adoptedStyleSheets=document.adoptedStyleSheets.concat([s]);}catch(e){}})()"

        private const val SCRAPE_JS =
            "(function(){" +
                "function cv(n){var v=getComputedStyle(document.body).getPropertyValue(n).trim();if(!v)return '';" +
                "var e=document.createElement('span');e.style.color=v;document.body.appendChild(e);var c=getComputedStyle(e).color;e.remove();return c;}" +
                "function pc(p){var m=document.querySelector('.admin-content')||document.body;return getComputedStyle(m)[p===\'color\'?\'color\':\'backgroundColor\'];}" +
                "function ic(root){var u=root.querySelector('use');var h=u?u.getAttribute('href'):'';return h&&h.indexOf('#')>=0?h.split('#')[1]:'';}" +
                "var side=document.querySelector('#admin-sidebar .sidebar-nav');" +
                "var drop=document.querySelector('.account-dropdown-menu');" +
                "var bell=document.querySelector('.fixed-auth-area .nk-root');" +
                "if(!side&&!drop&&!bell)return '';" +
                "var items=[];" +
                "if(side){side.querySelectorAll(':scope > li').forEach(function(li){" +
                "if(li.classList.contains('sidebar-nav-heading')){items.push({t:'h',l:li.textContent.trim()});return;}" +
                "var a=li.querySelector('a');if(!a)return;" +
                "items.push({t:'a',l:a.textContent.trim(),h:a.getAttribute('href')||'',i:ic(a),a:a.classList.contains('is-active')});});}" +
                "var acct=null;if(drop){var ai=[];drop.querySelectorAll('.account-dropdown-item').forEach(function(e,i){" +
                "ai.push({l:e.textContent.trim(),i:ic(e),x:i});});" +
                "var nm=document.querySelector('.account-name');var bt=document.querySelector('.account-dropdown-button');" +
                "acct={name:nm?nm.textContent.trim():'',label:bt?(bt.getAttribute('aria-label')||''):'',items:ai};}" +
                "var bl=null;if(bell){var bb=bell.querySelector('[aria-label]');var bg=bell.querySelector('.nk-badge');" +
                "bl={label:bb?(bb.getAttribute('aria-label')||''):'',unread:bg&&!bg.hidden?bg.textContent.trim():''};}" +
                "var tg=document.querySelector('.admin-topbar-nav-toggle');var th=document.querySelector('#admin-sidebar .sidebar-header h2');" +
                "var ds=document.getElementById('dswitch');" +
                "var o={colors:{sidebarBg:cv('--sidebar-bg'),sidebarText:cv('--sidebar-text'),accent:cv('--primary-color'),bg:pc('background-color'),text:pc('color')}," +
                "title:th?th.textContent.trim():'',menuLabel:tg?(tg.getAttribute('aria-label')||''):'',items:items,acct:acct,bell:bl};" +
                "if(ds)o.dark=!!ds.checked;" +
                "return JSON.stringify(o);})()"
    }
}
