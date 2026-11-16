# Android shell

A thin Android wrapper around a web application, shared by every app that needs one. The app supplies its own name, address pattern, colours, icons and splash through `ShellConfig` and resource overrides; this module holds everything else.

## What the shell does

- Asks for the account's subdomain once. The scheme and the rest of the host are fixed by the app, the person types only the subdomain. Input is lowercased and must match `[a-z0-9]([a-z0-9-]*[a-z0-9])?` (at most 63 characters), so dots, slashes, ports and wildcards cannot get in.
- Shows the web app in a WebView limited to that one host. Everything else opens in the system browser or the matching app (`tg:` links open Telegram).
- Splash screen through the AndroidX splash API. Override `shell_splash_background`, `shell_splash_icon` and `shell_notification_icon` in the app's resources.
- Push through UnifiedPush (a distributor app such as ntfy). Messages are standard Web Push, so the server uses the same sender as for browsers. The app registers the endpoint through the page, which keeps all session cookies inside the WebView.
- A message bridge for plugins. Only the instance origin can reach it (`addWebMessageListener` with an exact origin rule), and the page calls `NativeShell.call(plugin, action, args)`, which returns a promise of `{ok, result | error}`.

## Plugins

| Plugin | Actions | Notes |
| --- | --- | --- |
| `shell` | `info`, `change_instance` | |
| `push` | `register`, `state`, `ack` | `register` returns `{endpoint, p256dh, auth}` |
| `youtube` | `status`, `signin`, `collect`, `ack` | Optional (`youTubeSync`). Sign-in happens in a WebView on the device. A hidden WebView loads the watch history with those cookies and only video ids leave the phone. The cookies are never read by the page. |
| `scrobble` | `status`, `open_access`, `drain`, `ack` | Optional (`mediaScrobble`). Reads media sessions of an allowlist of apps through notification access. Events queue on the device until the page picks them up. |

## Using it in an app

1. Include the module from the app's `settings.gradle.kts`:

   ```kotlin
   include(":shell")
   project(":shell").projectDir = file("../../lib/android/shell")
   ```

2. Depend on it and install the config from `Application.onCreate`:

   ```kotlin
   Shell.install(this, ShellConfig(
       appName = getString(R.string.app_name),
       hostPrefix = "https://",
       hostSuffix = ".example.org",
       pushChannelName = getString(R.string.push_channel),
       youTubeSync = true,
       mediaScrobble = true,
   ))
   ```

3. Override colours and icons by defining resources with the same names in the app module.

An app that does not want media detection can remove the listener service from its manifest with `tools:node="remove"`.

## Building

There is no Android SDK on the servers. Build in CI (the runner image already has the SDK) with `gradle assembleRelease`; see the workflow in the consuming app.
