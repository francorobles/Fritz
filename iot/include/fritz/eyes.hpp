#pragma once

#include <chrono>
#include <random>
#include <string>

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

// Parses a lowercase emotion name ("normal", "thinking", ...), as used by
// the --emotion flag and the UDP face protocol. False on unknown names.
bool parse_emotion(const std::string& name, Emotion& out);

// Lowercase name for an emotion (inverse of parse_emotion).
const char* emotion_name(Emotion emotion);

// A pair of EVE-pattern eyes (rounded striped ovals on black).
// All expressions share blue ovals, fine scanlines, and a soft halo.
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

    // External gaze override (face protocol): look toward (x, y), each in
    // [-1, 1] (+x = screen right, +y = down). Overrides standby wandering
    // and the emotion's resting gaze, eases toward the target (~0.15 s),
    // and expires 1.5 s after the last call.
    void set_gaze_target(float x, float y);
    void clear_gaze_target();
    bool gaze_active() const { return gaze_override_; }

    // Speaking pulse (~4 Hz openness wobble) while TTS audio plays.
    void set_speaking(bool speaking) { speaking_ = speaking; }
    bool speaking() const { return speaking_; }

    // Trigger one blink on the next update.
    void blink_now() { blink_pending_ = true; }

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

    bool blink_pending_ = false;
    bool speaking_ = false;
    float speaking_pulse_ = 1.0f;  // openness scale, ~4 Hz while speaking

    // Gaze override state (see set_gaze_target).
    bool gaze_override_ = false;
    float gaze_override_x_ = 0.0f;
    float gaze_override_y_ = 0.0f;
    float gaze_target_x_ = 0.0f;
    float gaze_target_y_ = 0.0f;
    std::chrono::steady_clock::time_point gaze_deadline_{};
    float last_elapsed_s_ = 0.0f;
};

}  // namespace fritz
