#include "fritz/app.hpp"

#include <chrono>
#include <iostream>
#include <utility>

#include <SDL.h>

namespace fritz {
namespace {

SDL_Window* as_window(void* p) { return static_cast<SDL_Window*>(p); }
SDL_Renderer* as_renderer(void* p) { return static_cast<SDL_Renderer*>(p); }

}  // namespace

App::App(Options options) : options_(std::move(options)) {
    if (SDL_Init(SDL_INIT_VIDEO) != 0) {
        error_ = std::string("SDL_Init failed: ") + SDL_GetError();
        return;
    }

    const Uint32 flags = options_.fullscreen
                             ? SDL_WINDOW_FULLSCREEN_DESKTOP
                             : SDL_WINDOW_RESIZABLE;
    SDL_Window* window = SDL_CreateWindow(
        options_.title.c_str(), SDL_WINDOWPOS_CENTERED, SDL_WINDOWPOS_CENTERED,
        options_.width, options_.height, flags);
    if (window == nullptr) {
        error_ = std::string("SDL_CreateWindow failed: ") + SDL_GetError();
        SDL_Quit();
        return;
    }
    window_ = window;

    SDL_Renderer* renderer = SDL_CreateRenderer(
        window, -1, SDL_RENDERER_ACCELERATED | SDL_RENDERER_PRESENTVSYNC);
    if (renderer == nullptr) {
        // Fall back to software rendering (e.g. minimal KMSDRM setups).
        renderer = SDL_CreateRenderer(window, -1, SDL_RENDERER_SOFTWARE);
    }
    if (renderer == nullptr) {
        error_ = std::string("SDL_CreateRenderer failed: ") + SDL_GetError();
        SDL_DestroyWindow(window);
        window_ = nullptr;
        SDL_Quit();
        return;
    }
    renderer_ = renderer;

    eyes_.set_standby(true);  // standby face at startup
    SDL_ShowCursor(SDL_DISABLE);
}

App::~App() {
    if (renderer_ != nullptr) {
        SDL_DestroyRenderer(as_renderer(renderer_));
    }
    if (window_ != nullptr) {
        SDL_DestroyWindow(as_window(window_));
    }
    SDL_Quit();
}

void App::run() {
    if (!valid()) {
        return;
    }
    running_.store(true);

    auto last_tick = std::chrono::steady_clock::now();

    while (running_.load()) {
        SDL_Event event;
        while (SDL_PollEvent(&event) != 0) {
            if (event.type == SDL_QUIT) {
                running_.store(false);
            } else if (event.type == SDL_KEYDOWN) {
                const SDL_Keycode key = event.key.keysym.sym;
                if (key == SDLK_ESCAPE || key == SDLK_q) {
                    running_.store(false);
                }
            }
        }

        // Black background, then the face.
        SDL_Renderer* renderer = as_renderer(renderer_);
        int width = 0;
        int height = 0;
        SDL_GetRendererOutputSize(renderer, &width, &height);
        SDL_SetRenderDrawColor(renderer, 0, 0, 0, SDL_ALPHA_OPAQUE);
        SDL_RenderClear(renderer);
        eyes_.update();
        eyes_.draw(renderer, width, height);
        SDL_RenderPresent(renderer);

        // Remainder of frame pacing when vsync is unavailable.
        SDL_Delay(1);

        const auto now = std::chrono::steady_clock::now();
        if (now - last_tick >= std::chrono::seconds(5)) {
            std::cout << "heartbeat: running\n" << std::flush;
            last_tick = now;
        }
    }
}

}  // namespace fritz
