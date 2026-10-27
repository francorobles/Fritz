import SwiftUI

struct ContentView: View {
    var body: some View {
        VStack(spacing: 12) {
            Text("Hello from Fritz (iOS)")
                .font(.title3)
                .fontWeight(.semibold)
            Text("Native SwiftUI app — start building here.")
                .foregroundStyle(.secondary)
        }
        .padding()
    }
}

#Preview {
    ContentView()
}
