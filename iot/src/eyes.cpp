#include "fritz/eyes.hpp"

#include <algorithm>
#include <cmath>
#include <vector>

#include <SDL.h>

namespace fritz {
namespace {

constexpr float kPi = 3.14159265358979323846f;
constexpr SDL_Color kEyeColor{85, 155, 245, 255};
constexpr float kEyeExponent = 2.4f;
constexpr int kEyeStripes = 13;

// One expression, in fractions of min(viewport_w, viewport_h), adapted
// from the reference tutorial to EVE-pattern eyes (no pupils/brows, so
// openness/tilt/gaze carry the expression):
//   openness  how far the lids are open (Happy squints, Scared is wide)
//   tilt_deg  right-eye rotation; the left eye mirrors it. Negative
//             tilts the inner ends down (Angry/Focused).
//   gaze_*    resting look direction (Thinking looks up and to a side)
//   right_open  right eye openness relative to the left (Skeptical: one
//             eye squints, the other does not)
struct Shape {
    float half_width;
    float half_height;
    float openness;
    float tilt_deg;
    float center_dx;
    float center_dy;
    float gaze_x;
    float gaze_y;
    float right_openness;
};

Shape shape_for(Emotion emotion) {
    //          hw      hh     open   tilt    dx      dy      gx      gy     r-open
    switch (emotion) {
        case Emotion::Thinking:
            // No tilt: the mirrored tilt raises both inner ends, which
            // reads as worried brows (30° looked scared on the TFT).
            // Pondering comes from asymmetry and an upward drift
            // instead; center_dy is used even while vision overrides
            // the gaze, so the eyes still roll up mid-conversation.
            return {0.160f, 0.092f, 0.88f, 0.0f, 0.250f, -0.034f, 0.045f, -0.032f, 0.78f};
        case Emotion::Surprised:
            return {0.155f, 0.104f, 1.00f, 3.0f, 0.255f, -0.020f, 0.0f, -0.006f, 1.0f};
        case Emotion::Angry:
            return {0.164f, 0.070f, 0.72f, -18.0f, 0.258f, 0.004f, 0.0f, 0.004f, 1.0f};
        case Emotion::Skeptical:
            return {0.162f, 0.080f, 0.92f, -7.0f, 0.260f, 0.0f, 0.038f, 0.0f, 0.55f};
        case Emotion::Focused:
            return {0.166f, 0.072f, 0.78f, -10.0f, 0.258f, 0.0f, 0.0f, 0.0f, 1.0f};
        case Emotion::Scared:
            return {0.152f, 0.106f, 1.00f, 9.0f, 0.252f, -0.024f, 0.0f, -0.008f, 1.0f};
        case Emotion::Happy:
            return {0.164f, 0.076f, 0.60f, 10.0f, 0.260f, 0.006f, 0.0f, -0.004f, 1.0f};
        case Emotion::Tired:
            return {0.162f, 0.078f, 0.38f, 11.0f, 0.258f, 0.024f, 0.0f, 0.012f, 1.0f};
        case Emotion::Normal:
        default:
            // Calm, level blue ovals with horizontal scanlines. Keep the
            // pair symmetric; the reference photo is viewed at an angle.
            return {0.160f, 0.105f, 1.00f, 0.0f, 0.235f, 0.0f, 0.0f, 0.0f, 1.0f};
    }
}

SDL_FPoint rotate(float cx, float cy, float lx, float ly, float cos_a,
                  float sin_a) {
    return {cx + lx * cos_a - ly * sin_a, cy + lx * sin_a + ly * cos_a};
}

// Solid rounded-rectangular oval (superellipse) via a triangle fan.
// Used for the soft glow under the striped core.
void draw_oval(SDL_Renderer* renderer, float cx, float cy, float half_w,
               float half_h, float angle_deg, SDL_Color color,
               float exponent = kEyeExponent) {
    constexpr int kSegments = 64;

    const float rad = angle_deg * kPi / 180.0f;
    const float cos_a = std::cos(rad);
    const float sin_a = std::sin(rad);

    std::vector<SDL_Vertex> vertices;
    vertices.reserve(kSegments + 1);
    vertices.push_back({{cx, cy}, color, {0.0f, 0.0f}});
    for (int i = 0; i <= kSegments; ++i) {
        const float t = 2.0f * kPi * static_cast<float>(i) / kSegments;
        const float c = std::cos(t);
        const float s = std::sin(t);
        const float lx = half_w * std::copysign(std::pow(std::abs(c), 2.0f / exponent), c);
        const float ly = half_h * std::copysign(std::pow(std::abs(s), 2.0f / exponent), s);
        const SDL_FPoint p = rotate(cx, cy, lx, ly, cos_a, sin_a);
        vertices.push_back({p, color, {0.0f, 0.0f}});
    }

    std::vector<int> indices;
    indices.reserve(kSegments * 3);
    for (int i = 1; i <= kSegments; ++i) {
        indices.push_back(0);
        indices.push_back(i);
        indices.push_back(i + 1);
    }

    SDL_RenderGeometry(renderer, nullptr, vertices.data(),
                       static_cast<int>(vertices.size()), indices.data(),
                       static_cast<int>(indices.size()));
}

// One horizontal LED stripe of the eye core: a quad clipped to the oval's
// chord at that height, so black gaps show between stripes (EVE pattern).
void draw_stripe(SDL_Renderer* renderer, float cx, float cy, float half_w,
                 float half_h, float angle_deg, float y, float thickness,
                 SDL_Color color, float exponent) {
    const float ny = y / half_h;  // -1..1 across the eye
    if (std::abs(ny) >= 1.0f) {
        return;
    }
    const float chord =
        half_w * std::pow(1.0f - std::pow(std::abs(ny), exponent), 1.0f / exponent);
    if (chord <= 1.0f) {
        return;
    }

    const float rad = angle_deg * kPi / 180.0f;
    const float cos_a = std::cos(rad);
    const float sin_a = std::sin(rad);
    const float ht = thickness * 0.5f;

    const SDL_Vertex quad[] = {
        {rotate(cx, cy, -chord, y - ht, cos_a, sin_a), color, {0.0f, 0.0f}},
        {rotate(cx, cy, chord, y - ht, cos_a, sin_a), color, {0.0f, 0.0f}},
        {rotate(cx, cy, chord, y + ht, cos_a, sin_a), color, {0.0f, 0.0f}},
        {rotate(cx, cy, -chord, y + ht, cos_a, sin_a), color, {0.0f, 0.0f}},
    };
    const int indices[] = {0, 1, 2, 0, 2, 3};
    SDL_RenderGeometry(renderer, nullptr, quad, 4, indices, 6);
}

// Striped EVE eye: horizontal bars with black gaps. Bars are brighter
// in the middle and deeper toward the edges, as in the reference image.
void draw_eye(SDL_Renderer* renderer, float cx, float cy, float half_w,
              float half_h, float angle_deg, SDL_Color color,
              float exponent = kEyeExponent, int stripes = kEyeStripes) {
    const float pitch = (2.0f * half_h) / stripes;
    const float thickness = pitch * 0.60f;  // rest is black gap
    for (int i = 0; i < stripes; ++i) {
        const float y = -half_h + pitch * (static_cast<float>(i) + 0.5f);
        const float edge = std::abs(y) / half_h;  // 0 center, ~1 edge
        const float shade = 1.0f - 0.38f * edge;
        const SDL_Color bar{
            static_cast<Uint8>(color.r * shade),
            static_cast<Uint8>(color.g * shade),
            static_cast<Uint8>(color.b * shade),
            color.a,
        };
        draw_stripe(renderer, cx, cy, half_w, half_h, angle_deg, y, thickness,
                    bar, exponent);
    }
}

float smoothstep(float t) { return t * t * (3.0f - 2.0f * t); }

// Gaze override limits: the extremes of the standby gaze ranges below
// (Normal gx ±0.045 / Thinking gx ±0.062, gy −0.042…0.012), applied
// symmetrically so (1, 1) is the furthest comfortable look down-right.
constexpr float kGazeLimitX = 0.060f;
constexpr float kGazeLimitY = 0.040f;
constexpr float kGazeTimeConstantS = 0.15f;
constexpr auto kGazeExpiry = std::chrono::milliseconds(1500);

}  // namespace

bool parse_emotion(const std::string& name, Emotion& out) {
    if (name == "normal") {
        out = Emotion::Normal;
    } else if (name == "thinking") {
        out = Emotion::Thinking;
    } else if (name == "surprised") {
        out = Emotion::Surprised;
    } else if (name == "angry") {
        out = Emotion::Angry;
    } else if (name == "skeptical") {
        out = Emotion::Skeptical;
    } else if (name == "focused") {
        out = Emotion::Focused;
    } else if (name == "scared") {
        out = Emotion::Scared;
    } else if (name == "happy") {
        out = Emotion::Happy;
    } else if (name == "tired") {
        out = Emotion::Tired;
    } else {
        return false;
    }
    return true;
}

const char* emotion_name(Emotion emotion) {
    switch (emotion) {
        case Emotion::Thinking: return "thinking";
        case Emotion::Surprised: return "surprised";
        case Emotion::Angry: return "angry";
        case Emotion::Skeptical: return "skeptical";
        case Emotion::Focused: return "focused";
        case Emotion::Scared: return "scared";
        case Emotion::Happy: return "happy";
        case Emotion::Tired: return "tired";
        case Emotion::Normal:
        default: return "normal";
    }
}

Eyes::Eyes() : rng_(std::random_device{}()) {}

void Eyes::set_emotion(Emotion emotion) {
    emotion_ = emotion;
    standby_ = false;
    gaze_x_ = 0.0f;
    gaze_y_ = 0.0f;
    gaze_moving_ = false;
    clear_gaze_target();
}

void Eyes::set_standby(bool standby) {
    standby_ = standby;
    emotion_ = Emotion::Normal;
    clock_started_ = false;  // restart blink/gaze/standby timelines
    blinking_ = false;
    blink_openness_ = 1.0f;
    gaze_x_ = 0.0f;
    gaze_y_ = 0.0f;
    gaze_moving_ = false;
    clear_gaze_target();
}

void Eyes::set_gaze_target(float x, float y) {
    gaze_target_x_ = std::clamp(x, -1.0f, 1.0f) * kGazeLimitX;
    gaze_target_y_ = std::clamp(y, -1.0f, 1.0f) * kGazeLimitY;
    if (!gaze_override_) {
        // Start easing from wherever the eyes currently rest.
        gaze_override_x_ = standby_ ? gaze_x_ : shape_for(emotion_).gaze_x;
        gaze_override_y_ = standby_ ? gaze_y_ : shape_for(emotion_).gaze_y;
        gaze_override_ = true;
    }
    gaze_deadline_ = std::chrono::steady_clock::now() + kGazeExpiry;
}

void Eyes::clear_gaze_target() {
    gaze_override_ = false;
    gaze_override_x_ = 0.0f;
    gaze_override_y_ = 0.0f;
    gaze_target_x_ = 0.0f;
    gaze_target_y_ = 0.0f;
}

void Eyes::update() {
    const auto now = std::chrono::steady_clock::now();
    if (!clock_started_) {
        start_ = now;
        clock_started_ = true;
        std::uniform_real_distribution<float> first_blink(1.4f, 3.0f);
        std::uniform_real_distribution<float> first_gaze(1.8f, 3.2f);
        std::uniform_real_distribution<float> first_switch(5.0f, 8.0f);
        next_blink_s_ = first_blink(rng_);
        next_gaze_s_ = first_gaze(rng_);
        next_standby_switch_s_ = first_switch(rng_);
    }
    const float elapsed =
        std::chrono::duration<float>(now - start_).count();
    const float dt = std::max(0.0f, elapsed - last_elapsed_s_);
    last_elapsed_s_ = elapsed;

    // --- Requested blink (face protocol) -------------------------------
    if (blink_pending_ && !blinking_) {
        blink_pending_ = false;
        blinking_ = true;
        blink_start_s_ = elapsed;
    }

    // --- Gaze override: ease toward target, expire after 1.5 s ---------
    if (gaze_override_) {
        if (now >= gaze_deadline_) {
            clear_gaze_target();
        } else if (dt > 0.0f) {
            const float alpha = 1.0f - std::exp(-dt / kGazeTimeConstantS);
            gaze_override_x_ += (gaze_target_x_ - gaze_override_x_) * alpha;
            gaze_override_y_ += (gaze_target_y_ - gaze_override_y_) * alpha;
        }
    }

    // --- Speaking pulse: ~4 Hz openness wobble --------------------------
    speaking_pulse_ = speaking_
                          ? 1.0f + 0.07f * std::sin(2.0f * kPi * 4.0f * elapsed)
                          : 1.0f;

    // --- Human blink (all emotions): ~120–300 ms, every ~3–6 s --------
    const float blink_seconds = emotion_ == Emotion::Tired ? 0.30f : 0.15f;
    if (blinking_) {
        const float p = (elapsed - blink_start_s_) / blink_seconds;
        if (p >= 1.0f) {
            blinking_ = false;
            blink_openness_ = 1.0f;
            std::uniform_real_distribution<float> chance(0.0f, 1.0f);
            std::uniform_real_distribution<float> interval(3.0f, 6.0f);
            next_blink_s_ = elapsed + (chance(rng_) < 0.08f ? 0.30f
                                                            : interval(rng_));
        } else {
            blink_openness_ = 1.0f - 0.92f * std::sin(kPi * p);
        }
    } else if (elapsed >= next_blink_s_) {
        blinking_ = true;
        blink_start_s_ = elapsed;
    }

    if (!standby_) {
        return;
    }

    // --- Standby: alternate Normal and Thinking every few seconds -----
    if (elapsed >= next_standby_switch_s_) {
        std::uniform_real_distribution<float> normal_for(5.0f, 9.0f);
        std::uniform_real_distribution<float> thinking_for(4.0f, 7.0f);
        if (emotion_ == Emotion::Normal) {
            emotion_ = Emotion::Thinking;
            next_standby_switch_s_ = elapsed + thinking_for(rng_);
        } else {
            emotion_ = Emotion::Normal;
            next_standby_switch_s_ = elapsed + normal_for(rng_);
        }
        next_gaze_s_ = elapsed;  // look somewhere fitting the new face now
        gaze_moving_ = false;
    }

    // --- Standby gaze ---------------------------------------------------
    // Normal: small calm glances. Thinking: looks up/to a side and shifts
    // between a few up-side points, as if recalling something.
    constexpr float kGazeMoveSeconds = 0.45f;
    if (gaze_moving_) {
        const float p = (elapsed - gaze_move_start_s_) / kGazeMoveSeconds;
        if (p >= 1.0f) {
            gaze_moving_ = false;
            gaze_x_ = gaze_to_x_;
            gaze_y_ = gaze_to_y_;
            std::uniform_real_distribution<float> hold_normal(2.0f, 4.2f);
            std::uniform_real_distribution<float> hold_think(1.4f, 2.8f);
            next_gaze_s_ = elapsed + (emotion_ == Emotion::Thinking
                                          ? hold_think(rng_)
                                          : hold_normal(rng_));
        } else {
            const float e = smoothstep(p);
            gaze_x_ = gaze_from_x_ + (gaze_to_x_ - gaze_from_x_) * e;
            gaze_y_ = gaze_from_y_ + (gaze_to_y_ - gaze_from_y_) * e;
        }
    } else if (elapsed >= next_gaze_s_) {
        gaze_moving_ = true;
        gaze_move_start_s_ = elapsed;
        gaze_from_x_ = gaze_x_;
        gaze_from_y_ = gaze_y_;
        std::uniform_real_distribution<float> center(0.0f, 1.0f);
        if (emotion_ == Emotion::Thinking) {
            const float side = center(rng_) < 0.5f ? -1.0f : 1.0f;
            std::uniform_real_distribution<float> gx(0.030f, 0.062f);
            std::uniform_real_distribution<float> gy(-0.042f, -0.024f);
            gaze_to_x_ = side * gx(rng_);
            gaze_to_y_ = gy(rng_);
        } else {
            std::uniform_real_distribution<float> gx(-0.045f, 0.045f);
            std::uniform_real_distribution<float> gy(-0.016f, 0.012f);
            gaze_to_x_ = center(rng_) < 0.2f ? 0.0f : gx(rng_);
            gaze_to_y_ = gy(rng_);
        }
    }
}

void Eyes::draw(SDL_Renderer* renderer, int width, int height) const {
    if (renderer == nullptr || width <= 0 || height <= 0) {
        return;
    }

    const Shape shape = shape_for(emotion_);
    const float unit = static_cast<float>(std::min(width, height));
    // In standby the animated gaze replaces the shape's resting gaze,
    // so switching Normal <-> Thinking never pops the eyes sideways.
    // A gaze override (face protocol) wins over both.
    const float look_x = gaze_override_ ? gaze_override_x_
                                        : (standby_ ? gaze_x_ : shape.gaze_x);
    const float look_y = gaze_override_ ? gaze_override_y_
                                        : (standby_ ? gaze_y_ : shape.gaze_y);
    const float cx = width * 0.5f + look_x * unit;
    const float cy = height * 0.5f + (shape.center_dy + look_y) * unit;
    const float dx = shape.center_dx * unit;
    const float half_w = shape.half_width * unit;
    const float half_h_left = shape.half_height * shape.openness *
                              blink_openness_ * speaking_pulse_ * unit;
    const float half_h_right = half_h_left * shape.right_openness;

    SDL_SetRenderDrawBlendMode(renderer, SDL_BLENDMODE_BLEND);

    // Overlapping low-alpha shells follow each eye's tilt and openness.
    const SDL_Color glow{kEyeColor.r, kEyeColor.g, kEyeColor.b, 6};
    for (int layer = 8; layer >= 1; --layer) {
        const float spread = static_cast<float>(layer) / 8.0f;
        const float glow_w = half_w * (1.0f + 0.30f * spread);
        const float height_scale = 1.0f + 0.45f * spread;
        draw_oval(renderer, cx - dx, cy, glow_w,
                  half_h_left * height_scale, -shape.tilt_deg, glow);
        draw_oval(renderer, cx + dx, cy, glow_w,
                  half_h_right * height_scale, shape.tilt_deg, glow);
    }

    // Striped cores, mirrored tilt.
    if (half_h_left >= 2.0f) {
        draw_eye(renderer, cx - dx, cy, half_w, half_h_left, -shape.tilt_deg, kEyeColor);
    }
    if (half_h_right >= 2.0f) {
        draw_eye(renderer, cx + dx, cy, half_w, half_h_right, shape.tilt_deg, kEyeColor);
    }
}

}  // namespace fritz
