---
name: fritz-eyes
description: >
  The Fritz face: the C++/SDL2 program in iot/ that draws EVE-style eyes on the
  Pi display, the UDP face-command protocol the Go brain uses to drive it
  (emotion, gaze, speaking), and the mapping from assistant states to
  expressions. Use when editing iot/, adding an expression, implementing or
  calling the face protocol, making the eyes follow a person, or debugging why
  the face doesn't react.
---

# Fritz eyes (iot/)

## What exists

- `iot/src/eyes.cpp` — `shape_for(Emotion)` table (nine expressions),
  blinking, standby Normal↔Thinking cycle, standby gaze wandering with eased
  moves (`kGazeMoveSeconds`).
- `iot/src/app.cpp` — SDL loop: poll events → `eyes_.update()` →
  `eyes_.draw()` → present. Esc/Q quits.
- `iot/src/main.cpp` — CLI (`--windowed`, `--emotion NAME`), signals.
- Test on the desktop: `cmake -S iot -B iot/build && cmake --build iot/build
  && ./iot/build/fritz_iot --windowed`.

The face never calls the network or Meta APIs. It only obeys commands.

## Face protocol (UDP, 127.0.0.1:7070)

One command per datagram, ASCII, newline optional. Unknown commands are logged
and ignored — never crash on bad input.

| Command | Effect |
|---|---|
| `emotion <name>` | Pin an expression (`normal thinking surprised angry skeptical focused scared happy tired`). Leaves standby. |
| `standby` | Return to standby (Normal↔Thinking cycle, wandering gaze). |
| `gaze <x> <y>` | Look at a point. `x`,`y` in [-1, 1]; +x = screen right, +y = down. Eased, not snapped. Overrides wandering/emotion gaze. |
| `gaze off` | Drop the override now. |
| `speaking on` / `speaking off` | Subtle pulse (openness/brightness wobble ~4 Hz) while TTS audio plays. |
| `blink` | Trigger one blink now (good for acknowledging "Hey Fritz"). |

Gaze override expires 1.5 s after the last `gaze` command, so a crashed
camera loop doesn't leave the eyes stuck.

### Implementing it in C++

- Add `include/fritz/face_server.hpp` / `src/face_server.cpp`: a
  non-blocking UDP socket (`socket(AF_INET, SOCK_DGRAM)`, bind
  127.0.0.1, `O_NONBLOCK`). `poll()` drains all pending datagrams each frame
  and returns parsed commands; called from `App::run()` before
  `eyes_.update()`. No threads needed.
- Port from `--face-port N` (default 7070); `--face-port 0` disables it.
- Reuse `parse_emotion` from `main.cpp` — move it next to `Emotion` so both
  use it.
- Gaze: add `set_gaze_target(x, y)` / `clear_gaze_target()` on `Eyes`;
  map [-1, 1] to the existing gaze fraction range (the standby code's
  `gx`/`gy` ranges are the visual limits) and ease toward the target every
  frame (exponential smoothing, ~0.15 s time constant) so 1–2 Hz camera
  updates still look smooth.
- Keep POSIX socket code out of the headers, like SDL is kept out of
  `app.hpp`.
- Quick test from a shell: `echo "emotion happy" | nc -u -w0 127.0.0.1 7070`.

### Calling it from Go

`api/internal/face/` — `type Client` wrapping a `net.UDPConn` with methods
`Emotion(name)`, `Standby()`, `Gaze(x, y)`, `GazeOff()`, `Speaking(bool)`,
`Blink()`. Fire-and-forget: send errors are logged once, never returned to
the agent.

## Assistant state → expression

| Fritz state | Face |
|---|---|
| Idle, nobody present | `standby` |
| Person present, idle | `normal` + `gaze` following them |
| Heard wake phrase / listening | `blink`, then `focused` |
| Waiting on Muse Spark | `thinking` |
| Speaking a reply | previous expression + `speaking on` |
| Urgent email / important event | `surprised` |
| Task done, good news | `happy` |
| Error, couldn't do it | `skeptical` |
| User in deep work (holding notifications) | `focused` |
| Late night, suggesting to stop | `tired` |
| Something unexpected (unknown person, startled) | `scared` (use sparingly) |

`angry` stays unused unless the user asks for it — an angry assistant demos
badly.

Keep this table and the Go code in sync: define the mapping once in
`api/internal/face/states.go`.
