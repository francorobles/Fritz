#pragma once

#include <chrono>
#include <random>

struct SDL_Renderer;

namespace fritz {

// The nine eye expressions from the reference tutorial
// (easydrawingtips.com, "How to Draw Eye Expressions"):
//   Normal, Thinking (daydreaming), Surprised, Angry,
//   Skeptical (plotting), Focused, Scared, Happy, Tired (sad).
// Each maps to a row in shape_for() in src/eyes.cpp.
enum class Emotion {
    Normal,
    Thinking,
    Surprised,
    Angry,
    Skeptical,
    Focused,
    Scared,
    Happy,
    Tired,
};

// A pair of EVE-pattern eyes (rounded striped ovals on black).
// Normal/Thinking are light blue, as in the EVE reference image.
// Blinking runs in every emotion, like a human. In standby mode the
// face alternates between Normal and Thinking on its own.
// Call update() once per frame before draw().
class Eyes {
public:
    Eyes();

    // Shows one emotion and leaves standby mode.
    void set_emotion(Emotion emotion);
    Emotion emotion() const { return emotion_; }

    // Standby cycles Normal <-> Thinking; off pins Normal.
    void set_standby(bool standby);
    bool standby() const { return standby_; }

    // Advances blink, gaze, and standby-cycle animation.
    void update();

    // Draws both eyes centered in a width x height viewport.
    void draw(SDL_Renderer* renderer, int width, int height) const;

private:
    Emotion emotion_ = Emotion::Normal;
    bool standby_ = false;

    std::mt19937 rng_;
    bool clock_started_ = false;
    std::chrono::steady_clock::time_point start_{};

    float blink_openness_ = 1.0f;  // 1 open, ~0.08 mid-blink
    bool blinking_ = false;
    float blink_start_s_ = 0.0f;
    float next_blink_s_ = 2.5f;

    // Animated gaze offset (standby only), fractions of min(w, h).
    float gaze_x_ = 0.0f;
    float gaze_y_ = 0.0f;
    float gaze_from_x_ = 0.0f;
    float gaze_from_y_ = 0.0f;
    float gaze_to_x_ = 0.0f;
    float gaze_to_y_ = 0.0f;
    bool gaze_moving_ = false;
    float gaze_move_start_s_ = 0.0f;
    float next_gaze_s_ = 1.2f;

    float next_standby_switch_s_ = 6.0f;
};

}  // namespace fritz
