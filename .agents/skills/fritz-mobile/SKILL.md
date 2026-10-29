---
name: fritz-mobile
description: >
  The Fritz Android companion app in android/ (Kotlin + Jetpack Compose): push
  alerts from Fritz when the user is away from the desk, Approve/Decline for
  pending actions (from the notification and in-app), push-to-talk to Fritz
  from the phone, the activity feed, and the settings/pairing screen. Use when
  working in android/, adding Firebase Cloud Messaging to the app, building
  notification actions, recording audio on Android, or calling the Fritz API
  from the phone. iOS (ios/) is out of scope.
---

# Fritz Android companion

The Muse app (and so Muse Gadgets) isn't available in the Philippines, so
Fritz reaches the user through its own Android app. The app is a thin client:
no model calls, no business logic. Everything goes through the Go API —
see `fritz-mobile-backend` for the server side and the exact API contract.

## What exists

`android/` — single-module Compose app, package `com.fritz.android`,
compile/target SDK 35, min SDK 26, AGP 8.7.3, Kotlin 2.0.21, Gradle 8.9,
Java 17. `MainActivity.kt` is a placeholder "Hello" screen.

Build: `cd android && ./gradlew :app:assembleDebug`, install on the phone
with `./gradlew :app:installDebug` (USB debugging on). Debug builds are
enough for the hackathon; no Play Store.

## Scope — three features, in this order

1. **Push alerts + Approve/Decline** (the demo moment)
2. **Activity feed + status** (is the user at the desk, Fritz's current face)
3. **Push-to-talk** (only if 1–2 are done)

## Screens (Compose, Material 3)

- **Home:** status card (`At desk` / `Away`, last seen, current emotion as a
  small drawing of the two blue eyes is a nice touch but optional), a
  "Needs your OK" list of pending actions with Approve/Decline buttons, then
  the activity feed (latest 50, pull to refresh).
- **Settings:** base URL and app token fields, "Test connection" button
  (`GET /status`), and the notification permission state. Shown on first
  launch until a connection succeeds.
- **Push-to-talk:** a big hold-to-talk button on Home; shows the transcript
  and Fritz's reply text, and reads the reply aloud with Android's on-device
  `TextToSpeech` (Fritz at the desk stays quiet when the user is away).

Keep it to one Activity, a `ViewModel` per screen, and `StateFlow` for UI
state. No navigation library is needed for two screens; a simple
`when(screen)` is fine.

## Talking to the API

- HTTP: OkHttp + `kotlinx.serialization` (add the
  `org.jetbrains.kotlin.plugin.serialization` plugin at the same Kotlin version,
  2.0.21). Coroutines via `viewModelScope`; never block the main thread.
- Every request sends `Authorization: Bearer <app token>`.
- Base URL is the Pi's Tailscale HTTPS name, e.g.
  `https://fritz.<tailnet>.ts.net` (set up by `tailscale serve`, see
  `fritz-mobile-backend`). Because it's HTTPS, no cleartext network
  security config is needed. Don't hardcode it — it comes from Settings.
- Store base URL, token, and FCM token in Jetpack DataStore (Preferences).
- Timeouts: 10 s normal calls, 60 s for push-to-talk.
- Show errors as a short Snackbar ("Can't reach Fritz — is Tailscale on?"),
  never a crash.

## Push notifications (Firebase Cloud Messaging)

Setup (one time, by the user): create a Firebase project, add an Android app
with package `com.fritz.android`, download `google-services.json` to
`android/app/`. **`google-services.json` must be gitignored** (the repo is
public); commit `android/app/google-services.json.example` noting where to get
it.

Gradle: add the `com.google.gms.google-services` plugin and the Firebase BOM
with `firebase-messaging` (no version on the artifact; look up the current
BOM and plugin versions in Firebase's Android setup docs rather than
guessing).

Implementation:

- `FritzMessagingService : FirebaseMessagingService`
  - `onNewToken` → save it and `POST /mobile/register`.
  - `onMessageReceived` → build the notification yourself. The server sends
    **data-only** high-priority messages so this runs even when the app is in
    the background (notification-type messages would be shown by the system
    without our action buttons).
- Notification channels: `alerts` (high importance) and `actions` (high
  importance). Create them at app start.
- For `type == "action"`: add **Approve** and **Decline** notification
  actions whose `PendingIntent`s target `ActionReceiver : BroadcastReceiver`.
  The receiver calls `POST /actions/{id}/approve|decline` using
  `goAsync()` + a coroutine, then replaces the notification with the result
  ("Sent ✓" / "Cancelled"). Use `PendingIntent.FLAG_IMMUTABLE` and a unique
  request code per action id.
- Tapping the notification opens Home scrolled to that action.
- Runtime permission: `POST_NOTIFICATIONS` (Android 13+), requested on the
  Settings screen with an explanation, not at launch.

Manifest additions: `INTERNET`, `POST_NOTIFICATIONS`, `RECORD_AUDIO` (for
push-to-talk), the messaging service with the `com.google.firebase.MESSAGING_EVENT`
intent filter, and the receiver (`exported="false"`).

## Push-to-talk recording

- `AudioRecord` with `MediaRecorder.AudioSource.VOICE_RECOGNITION`, 16 kHz,
  mono, `ENCODING_PCM_16BIT` — exactly what Muse Voice Transcribe wants, so no
  conversion on the server.
- Record while the button is held (cap at 30 s), prepend a 44-byte WAV
  header, upload as multipart to `POST /voice/turn`.
- Request `RECORD_AUDIO` the first time the button is pressed.

## Testing

- Unit tests (JVM) for the WAV header writer and JSON models.
- Manual checklist on a real phone: register token → server sends a test
  push (`POST /mobile/test-push`) → notification appears with buttons while
  the app is closed → Approve works from the lock screen.
- Build must pass: `./gradlew :app:assembleDebug :app:testDebugUnitTest`.
