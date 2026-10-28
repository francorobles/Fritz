#pragma once

#include <atomic>
#include <string>

#include "fritz/eyes.hpp"

namespace fritz {

// Fullscreen SDL2 window for the Pi display, showing the Eyes face.
// Desktop: fullscreen over X11/Wayland. Kiosk (no desktop): SDL's KMSDRM
// backend takes the display directly. Windowed mode is for development.
class App {
public:
    struct Options {
        bool fullscreen = true;
        int width = 1280;
        int height = 720;
        std::string title = "Fritz";
    };

    explicit App(Options options);
    ~App();

    App(const App&) = delete;
    App& operator=(const App&) = delete;

    // False if SDL/window/renderer creation failed; see error().
    bool valid() const { return window_ != nullptr && renderer_ != nullptr; }
    std::string error() const { return error_; }

    // Event + render loop. Returns on quit (Esc/Q/window close) or stop().
    // Logs a heartbeat line every 5 seconds while running.
    void run();
    void stop() { running_.store(false); }

    // Face shown on screen. The app starts in standby.
    // Pinning an emotion leaves standby mode.
    void set_emotion(Emotion emotion) {
        standby_ = false;
        eyes_.set_emotion(emotion);
    }

    // Standby alternates Normal and Thinking eyes, with human blinking.
    void set_standby(bool standby) {
        standby_ = standby;
        eyes_.set_standby(standby);
    }
    bool standby() const { return standby_; }

private:
    Options options_;
    Eyes eyes_;
    bool standby_ = true;
    std::string error_;
    std::atomic<bool> running_{false};

    // Opaque SDL handles (SDL_Window/SDL_Renderer) to keep SDL out of headers.
    void* window_ = nullptr;
    void* renderer_ = nullptr;
};

}  // namespace fritz
