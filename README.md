# Fritz

Meta Global AI Hackathon Personal Assistant

## Subprojects

| Folder      | Stack                              | Run                                        |
| ----------- | ---------------------------------- | ------------------------------------------ |
| `android/`  | Kotlin + Jetpack Compose           | `./gradlew :app:assembleDebug` (in folder) |
| `ios/`      | Swift + SwiftUI                    | `open Fritz.xcodeproj`, then Run           |
| `iot/`      | C++17 / CMake (Raspberry Pi 4B)    | `cmake -B build && cmake --build build`    |
| `api/`      | Go + chi                           | `go run ./cmd/server` → `:8080`            |
| `web/`      | Next.js + React + TypeScript       | `npm run dev` → `http://localhost:3000`    |

Each subfolder has its own README with full setup and layout notes.
The clients (`android/`, `ios/`, `iot/`, `web/`) talk to the Go API in `api/`.
