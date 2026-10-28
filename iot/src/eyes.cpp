#include "fritz/eyes.hpp"

#include <algorithm>
#include <cmath>
#include <vector>

#include <SDL.h>

namespace fritz {
namespace {

constexpr float kPi = 3.14159265358979323846f;

// One expression, in fractions of min(viewport_w, viewport_h), adapted
// from the reference tutorial to EVE-pattern eyes (no pupils/brows, so
// openness/tilt/gaze carry the expression):
//   openness  how far the lids are open (Happy squints, Scared is wide)
//   tilt_deg  right-eye rotation; the left eye mirrors it. Negative
//             tilts the inner ends down (Angry/Focused).
//   gaze_*    resting look direction (Thinking looks up and to a side)
//   right_open  right eye openness relative to the left (Skeptical: one
//             eye squints, the other does not)
//   r/g/b     eye color. Normal (and Thinking, its standby partner)
//             use EVE's light blue from the reference image; the other
//             expressions stay white for now.
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
    Uint8 r;
    Uint8 g;
    Uint8 b;
};

Shape shape_for(Emotion emotion) {
    //          hw      hh     open   tilt    dx      dy      gx      gy     r-open  color (r, g, b)
    switch (emotion) {
        case Emotion::Thinking:
            return {0.160f, 0.080f, 0.95f, 30.0f, 0.260f, -0.006f, 0.045f, -0.032f, 1.0f, 125, 216, 255};
        case Emotion::Surprised:
            return {0.155f, 0.104f, 1.00f, 3.0f, 0.255f, -0.020f, 0.0f, -0.006f, 1.0f, 255, 255, 255};
        case Emotion::Angry:
            return {0.164f, 0.070f, 0.72f, -18.0f, 0.258f, 0.004f, 0.0f, 0.004f, 1.0f, 255, 255, 255};
        case Emotion::Skeptical:
            return {0.162f, 0.080f, 0.92f, -7.0f, 0.260f, 0.0f, 0.038f, 0.0f, 0.55f, 255, 255, 255};
        case Emotion::Focused:
            return {0.166f, 0.072f, 0.78f, -10.0f, 0.258f, 0.0f, 0.0f, 0.0f, 1.0f, 255, 255, 255};
        case Emotion::Scared:
            return {0.152f, 0.106f, 1.00f, 9.0f, 0.252f, -0.024f, 0.0f, -0.008f, 1.0f, 255, 255, 255};
        case Emotion::Happy:
            return {0.164f, 0.076f, 0.60f, 10.0f, 0.260f, 0.006f, 0.0f, -0.004f, 1.0f, 255, 255, 255};
        case Emotion::Tired:
            return {0.162f, 0.078f, 0.38f, 11.0f, 0.258f, 0.024f, 0.0f, 0.012f, 1.0f, 255, 255, 255};
        case Emotion::Normal:
        default:
            // EVE reference image: light blue striped eyes, edges at 30°
            // (left eye -30°, right eye +30°, mirrored).
            return {0.160f, 0.080f, 0.95f, 30.0f, 0.260f, 0.0f, 0.0f, 0.0f, 1.0f, 125, 216, 255};
    }
}

SDL_FPoint rotate(float cx, float cy, float lx, float ly, float cos_a,
                  float sin_a) {
    return {cx + lx * cos_a - ly * sin_a, cy + lx * sin_a + ly * cos_a};
}

// Solid rounded-rectangular oval (superellipse) via a triangle fan.
// Used for the soft glow under the striped core.
void draw_oval(SDL_Renderer* renderer, float cx, float cy, float half_w,
               float half_h, float angle_deg, SDL_Color color) {
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
        const float lx = half_w * std::copysign(std::pow(std::abs(c), 0.5f), c);
        const float ly = half_h * std::copysign(std::pow(std::abs(s), 0.5f), s);
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
                 SDL_Color color) {
    const float ny = y / half_h;  // -1..1 across the eye
    if (std::abs(ny) >= 1.0f) {
        return;
    }
    const float chord =
        half_w * std::pow(1.0f - std::pow(std::abs(ny), 4.0f), 0.25f);
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
              float half_h, float angle_deg, SDL_Color color) {
    constexpr int kStripes = 9;
    const float pitch = (2.0f * half_h) / kStripes;
    const float thickness = pitch * 0.60f;  // rest is black gap
    for (int i = 0; i < kStripes; ++i) {
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
                    bar);
    }
}

float smoothstep(float t) { return t * t * (3.0f - 2.0f * t); }

}  // namespace

Eyes::Eyes() : rng_(std::random_device{}()) {}

void Eyes::set_emotion(Emotion emotion) {
    emotion_ = emotion;
    standby_ = false;
    gaze_x_ = 0.0f;
    gaze_y_ = 0.0f;
    gaze_moving_ = false;
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
    const float look_x = standby_ ? gaze_x_ : shape.gaze_x;
    const float look_y = standby_ ? gaze_y_ : shape.gaze_y;
    const float cx = width * 0.5f + look_x * unit;
    const float cy = height * 0.5f + (shape.center_dy + look_y) * unit;
    const float dx = shape.center_dx * unit;
    const float half_w = shape.half_width * unit;
    const float half_h_left = shape.half_height * shape.openness *
                              blink_openness_ * unit;
    const float half_h_right = half_h_left * shape.right_openness;

    SDL_SetRenderDrawBlendMode(renderer, SDL_BLENDMODE_BLEND);

    // Soft glow behind both eyes (sized to the taller eye).
    const SDL_Color glow{shape.r, shape.g, shape.b, 30};
    const float glow_h = std::max(half_h_left, half_h_right);
    draw_oval(renderer, cx - dx, cy, half_w * 1.28f, glow_h * 1.45f,
              -shape.tilt_deg, glow);
    draw_oval(renderer, cx + dx, cy, half_w * 1.28f, glow_h * 1.45f,
              shape.tilt_deg, glow);

    // Striped cores, mirrored tilt.
    const SDL_Color core{shape.r, shape.g, shape.b, 255};
    if (half_h_left >= 2.0f) {
        draw_eye(renderer, cx - dx, cy, half_w, half_h_left, -shape.tilt_deg, core);
    }
    if (half_h_right >= 2.0f) {
        draw_eye(renderer, cx + dx, cy, half_w, half_h_right, shape.tilt_deg, core);
    }
}

}  // namespace fritz
