package com.csuit.shell

import android.content.Intent
import android.graphics.Typeface
import android.os.Bundle
import android.text.InputFilter
import android.text.InputType
import android.view.Gravity
import android.view.ViewGroup
import android.view.inputmethod.EditorInfo
import android.widget.Button
import android.widget.EditText
import android.widget.LinearLayout
import android.widget.TextView
import androidx.appcompat.app.AppCompatActivity
import androidx.core.content.ContextCompat

class InstanceActivity : AppCompatActivity() {
    private lateinit var input: EditText
    private lateinit var error: TextView

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        val config = Shell.config
        val pad = (24 * resources.displayMetrics.density).toInt()
        val text = ContextCompat.getColor(this, R.color.shell_on_background)

        val root = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            gravity = Gravity.CENTER_VERTICAL
            setPadding(pad, pad, pad, pad)
            setBackgroundColor(ContextCompat.getColor(this@InstanceActivity, R.color.shell_background))
        }

        val name = TextView(this).apply {
            this.text = config.appName
            textSize = 32f
            typeface = Typeface.DEFAULT_BOLD
            setTextColor(ContextCompat.getColor(this@InstanceActivity, R.color.shell_accent))
        }
        val prompt = TextView(this).apply {
            setText(R.string.shell_instance_title)
            textSize = 18f
            setTextColor(text)
            setPadding(0, pad / 2, 0, pad / 2)
        }

        input = EditText(this).apply {
            setHint(R.string.shell_instance_hint)
            setTextColor(text)
            setHintTextColor(ContextCompat.getColor(this@InstanceActivity, R.color.shell_on_background) and 0x80FFFFFF.toInt())
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
            textSize = 16f
            setTextColor(text)
        }
        val row = LinearLayout(this).apply {
            orientation = LinearLayout.HORIZONTAL
            gravity = Gravity.CENTER_VERTICAL
            addView(input)
            addView(suffix)
        }

        error = TextView(this).apply {
            setTextColor(ContextCompat.getColor(this@InstanceActivity, R.color.shell_error))
            setPadding(0, pad / 4, 0, pad / 4)
            visibility = android.view.View.GONE
        }

        val button = Button(this).apply {
            setText(R.string.shell_instance_continue)
            setTextColor(ContextCompat.getColor(this@InstanceActivity, R.color.shell_on_accent))
            setBackgroundColor(ContextCompat.getColor(this@InstanceActivity, R.color.shell_accent))
            setOnClickListener { submit() }
        }

        root.addView(name)
        root.addView(prompt)
        root.addView(row)
        root.addView(error)
        root.addView(button)
        setContentView(root)
    }

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
