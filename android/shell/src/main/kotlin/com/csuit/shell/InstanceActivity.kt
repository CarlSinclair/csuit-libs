package com.csuit.shell

import android.content.Intent
import android.graphics.Color
import android.graphics.Typeface
import android.graphics.drawable.GradientDrawable
import android.util.Log
import android.os.Bundle
import android.text.InputFilter
import android.text.InputType
import android.view.Gravity
import android.view.ViewGroup
import android.view.inputmethod.EditorInfo
import android.widget.Button
import android.widget.EditText
import android.widget.ImageView
import android.widget.LinearLayout
import android.widget.TextView
import androidx.appcompat.app.AppCompatActivity
import androidx.core.content.ContextCompat
import androidx.core.view.ViewCompat
import androidx.core.view.WindowCompat
import androidx.core.view.WindowInsetsCompat

class InstanceActivity : AppCompatActivity() {
    private lateinit var input: EditText
    private lateinit var error: TextView

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        WindowCompat.setDecorFitsSystemWindows(window, false)
        val config = Shell.config
        val density = resources.displayMetrics.density
        fun dp(v: Int) = (v * density).toInt()
        val text = ContextCompat.getColor(this, R.color.shell_on_background)
        val accent = ContextCompat.getColor(this, R.color.shell_accent)
        val background = ContextCompat.getColor(this, R.color.shell_background)

        val root = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            gravity = Gravity.CENTER
            setPadding(dp(28), dp(28), dp(28), dp(28))
            setBackgroundColor(background)
        }
        ViewCompat.setOnApplyWindowInsetsListener(root) { v, insets ->
            val bars = insets.getInsets(WindowInsetsCompat.Type.systemBars() or WindowInsetsCompat.Type.displayCutout() or WindowInsetsCompat.Type.ime())
            v.setPadding(dp(28) + bars.left, dp(28) + bars.top, dp(28) + bars.right, dp(28) + bars.bottom)
            WindowInsetsCompat.CONSUMED
        }

        val logo = ImageView(this).apply {
            try {
                setImageDrawable(packageManager.getApplicationIcon(packageName))
            } catch (e: Exception) {
                Log.w("Shell", "app icon unavailable for the address screen", e)
            }
            layoutParams = LinearLayout.LayoutParams(dp(96), dp(96)).apply { bottomMargin = dp(16) }
        }
        val name = TextView(this).apply {
            this.text = config.appName
            textSize = 28f
            gravity = Gravity.CENTER
            typeface = Typeface.DEFAULT_BOLD
            setTextColor(text)
        }
        val prompt = TextView(this).apply {
            setText(R.string.shell_instance_title)
            textSize = 16f
            gravity = Gravity.CENTER
            setTextColor(withAlpha(text, 170))
            setPadding(0, dp(8), 0, dp(24))
        }

        input = EditText(this).apply {
            setHint(R.string.shell_instance_hint)
            setTextColor(text)
            setHintTextColor(withAlpha(text, 110))
            this.background = null
            textSize = 17f
            inputType = InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_VARIATION_URI or InputType.TYPE_TEXT_FLAG_NO_SUGGESTIONS
            imeOptions = EditorInfo.IME_ACTION_DONE
            isSingleLine = true
            filters = arrayOf(InputFilter.LengthFilter(63))
            layoutParams = LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f)
            setOnEditorActionListener { _, actionId, _ ->
                if (actionId == EditorInfo.IME_ACTION_DONE) {
                    submit()
                    true
                } else {
                    false
                }
            }
        }
        val suffix = TextView(this).apply {
            this.text = config.hostSuffix
            textSize = 17f
            setTextColor(withAlpha(text, 170))
        }
        val field = GradientDrawable().apply {
            cornerRadius = dp(14).toFloat()
            setColor(withAlpha(text, 24))
            setStroke(dp(1), withAlpha(text, 60))
        }
        val row = LinearLayout(this).apply {
            orientation = LinearLayout.HORIZONTAL
            gravity = Gravity.CENTER_VERTICAL
            setPadding(dp(16), dp(6), dp(16), dp(6))
            this.background = field
            addView(input)
            addView(suffix)
        }

        error = TextView(this).apply {
            setTextColor(ContextCompat.getColor(this@InstanceActivity, R.color.shell_error))
            setPadding(dp(4), dp(8), dp(4), 0)
            visibility = android.view.View.GONE
        }

        val buttonBg = GradientDrawable().apply {
            cornerRadius = dp(14).toFloat()
            setColor(accent)
        }
        val button = Button(this).apply {
            setText(R.string.shell_instance_continue)
            setTextColor(ContextCompat.getColor(this@InstanceActivity, R.color.shell_on_accent))
            this.background = buttonBg
            isAllCaps = false
            textSize = 16f
            setOnClickListener { submit() }
            layoutParams = LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, dp(52)).apply { topMargin = dp(20) }
        }

        root.addView(logo)
        root.addView(name)
        root.addView(prompt)
        root.addView(row, LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT))
        root.addView(error, LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT))
        root.addView(button)
        setContentView(root)
    }

    private fun withAlpha(c: Int, a: Int): Int = Color.argb(a, Color.red(c), Color.green(c), Color.blue(c))

    private fun submit() {
        val clean = Subdomain.sanitize(input.text.toString())
        if (clean == null) {
            error.setText(R.string.shell_instance_invalid)
            error.visibility = android.view.View.VISIBLE
            return
        }
        InstanceStore(this).save(clean)
        startActivity(Intent(this, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_CLEAR_TOP))
        finish()
    }
}
