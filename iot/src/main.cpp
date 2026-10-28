#include <csignal>
#include <iostream>
#include <string>

#include "fritz/app.hpp"
#include "fritz/device.hpp"

namespace {

fritz::App* g_app = nullptr;

void handle_signal(int) {
    if (g_app != nullptr) {
        g_app->stop();
    }
}

void print_usage(const char* argv0) {
    std::cout << "Usage: " << argv0 << " [--windowed] [--width N] [--height N]\n"
              << "              [--emotion NAME]\n"
              << "  (no args)    fullscreen standby (Pi desktop or kiosk)\n"
              << "  --windowed   windowed mode for development on a desktop\n"
              << "  --emotion    pin one face instead of standby: normal,\n"
              << "               thinking, surprised, angry, skeptical,\n"
              << "               focused, scared, happy, tired\n"
              << "Quit with Esc or Q.\n";
}

bool parse_emotion(const std::string& name, fritz::Emotion& out) {
    if (name == "normal") {
        out = fritz::Emotion::Normal;
    } else if (name == "thinking") {
        out = fritz::Emotion::Thinking;
    } else if (name == "surprised") {
        out = fritz::Emotion::Surprised;
    } else if (name == "angry") {
        out = fritz::Emotion::Angry;
    } else if (name == "skeptical") {
        out = fritz::Emotion::Skeptical;
    } else if (name == "focused") {
        out = fritz::Emotion::Focused;
    } else if (name == "scared") {
        out = fritz::Emotion::Scared;
    } else if (name == "happy") {
        out = fritz::Emotion::Happy;
    } else if (name == "tired") {
        out = fritz::Emotion::Tired;
    } else {
        return false;
    }
    return true;
}

// Returns false on a bad argument (already reported).
bool parse_int(const char* text, int& out) {
    try {
        std::size_t used = 0;
        const int value = std::stoi(text, &used);
        if (used == 0 || text[used] != '\0' || value <= 0) {
            return false;
        }
        out = value;
        return true;
    } catch (...) {
        return false;
    }
}

}  // namespace

int main(int argc, char* argv[]) {
    fritz::App::Options options;
    bool has_emotion = false;
    fritz::Emotion emotion = fritz::Emotion::Normal;
    std::string emotion_name;

    for (int i = 1; i < argc; ++i) {
        const std::string arg = argv[i];
        if (arg == "--help" || arg == "-h") {
            print_usage(argv[0]);
            return 0;
        } else if (arg == "--windowed") {
            options.fullscreen = false;
        } else if (arg == "--width" && i + 1 < argc) {
            if (!parse_int(argv[++i], options.width)) {
                std::cerr << "Invalid --width value\n";
                return 2;
            }
        } else if (arg == "--height" && i + 1 < argc) {
            if (!parse_int(argv[++i], options.height)) {
                std::cerr << "Invalid --height value\n";
                return 2;
            }
        } else if (arg == "--emotion" && i + 1 < argc) {
            emotion_name = argv[++i];
            if (!parse_emotion(emotion_name, emotion)) {
                std::cerr << "Unknown emotion: " << emotion_name << '\n';
                print_usage(argv[0]);
                return 2;
            }
            has_emotion = true;
        } else {
            std::cerr << "Unknown argument: " << arg << '\n';
            print_usage(argv[0]);
            return 2;
        }
    }

    const fritz::Device device;
    std::cout << "Fritz IoT starting: " << device.status() << '\n';
#ifdef FRITZ_IOT_ENABLE_GPIO
    std::cout << "GPIO: enabled (no pins configured yet)\n";
#else
    std::cout << "GPIO: disabled (build with -DFRITZ_IOT_ENABLE_GPIO=ON on the Pi)\n";
#endif
    std::cout << (options.fullscreen ? "Display: fullscreen\n" : "Display: windowed\n");

    fritz::App app(options);
    if (!app.valid()) {
        std::cerr << "Failed to open display: " << app.error() << '\n'
                  << "On a desktop, check DISPLAY/WAYLAND_DISPLAY. "
                     "In kiosk mode, check /dev/dri access (video/render groups).\n";
        return 1;
    }

    if (has_emotion) {
        app.set_emotion(emotion);
        std::cout << "Mode: emotion (" << emotion_name << ")\n";
    } else {
        std::cout << "Mode: standby (normal/thinking eyes)\n";
    }

    g_app = &app;
    std::signal(SIGINT, handle_signal);
    std::signal(SIGTERM, handle_signal);

    app.run();

    g_app = nullptr;
    std::cout << "Fritz IoT shutting down\n";
    return 0;
}
