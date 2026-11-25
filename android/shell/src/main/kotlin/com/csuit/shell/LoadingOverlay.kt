package com.csuit.shell

import android.graphics.Color
import android.graphics.drawable.GradientDrawable
import android.os.Handler
import android.os.Looper
import android.view.Gravity
import android.view.View
import android.widget.FrameLayout
import android.widget.ProgressBar
import androidx.appcompat.app.AppCompatActivity

class LoadingOverlay(activity: AppCompatActivity) : FrameLayout(activity) {
    private val handler = Handler(Looper.getMainLooper())
    private val showNow = Runnable { visibility = View.VISIBLE }
    private val density = activity.resources.displayMetrics.density

    init {
        setBackgroundColor(0x66000000)
        visibility = View.GONE
        isClickable = true
        val card = FrameLayout(activity)
        val bg = GradientDrawable()
        bg.cornerRadius = 16 * density
        bg.setColor(0xF2202531.toInt())
        card.background = bg
        card.elevation = 12 * density
        val bar = ProgressBar(activity)
        bar.isIndeterminate = true
        bar.indeterminateTintList = android.content.res.ColorStateList.valueOf(Color.WHITE)
        val pad = (20 * density).toInt()
        card.setPadding(pad, pad, pad, pad)
        card.addView(bar, LayoutParams((36 * density).toInt(), (36 * density).toInt()))
        addView(card, LayoutParams(LayoutParams.WRAP_CONTENT, LayoutParams.WRAP_CONTENT, Gravity.CENTER))
    }

    fun show(delayMs: Long) {
        handler.removeCallbacks(showNow)
        if (delayMs <= 0) showNow.run() else handler.postDelayed(showNow, delayMs)
    }

    fun hide() {
        handler.removeCallbacks(showNow)
        visibility = View.GONE
    }
}
