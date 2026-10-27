# Fritz — iOS (Swift)

Native iOS app in Swift with SwiftUI (iOS 17+, Swift 5).

## Run

```sh
open Fritz.xcodeproj   # then Run (⌘R) on a simulator or device
```

Command-line build (no signing):

```sh
xcodebuild -project Fritz.xcodeproj -scheme Fritz \
  -sdk iphoneos CODE_SIGNING_ALLOWED=NO build
```

## Layout

- `Fritz/FritzApp.swift` — `@main` app entry point
- `Fritz/ContentView.swift` — initial SwiftUI view
- Bundle ID: `com.fritz.ios`
