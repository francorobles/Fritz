# Fritz — Android (Kotlin)

Native Android app in Kotlin with Jetpack Compose.

## Run

Open this `android/` folder in Android Studio and run the `app` configuration,
or from the command line via the included Gradle wrapper (8.9):

```sh
./gradlew :app:assembleDebug   # build a debug APK
./gradlew :app:installDebug    # install on a connected device/emulator
```

## Layout

- `app/src/main/java/com/fritz/android/MainActivity.kt` — entry point (Compose UI)
- `app/build.gradle.kts` — app module config (compile/target SDK 35, min SDK 26)
