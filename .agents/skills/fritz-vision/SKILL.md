---
name: fritz-vision
description: >
  Fritz's camera: capturing frames from the Raspberry Pi camera, detecting
  whether the user is present, making the eyes follow the user's face with SAM
  3.1, and vision tools for the agent ("look at this", "where are my keys?").
  Use when working on camera capture, presence detection, gaze tracking,
  api/internal/vision, or any feature that uses SAM 3.1 or Muse Spark image
  input.
---

# Fritz vision

Vision lives in Go (`api/internal/vision/`). It feeds three things: the face
(gaze), the voice loop (only listen when someone is there), and the agent
(vision tools). Load `meta-model-api` for request formats.

## Capture

The Pi 4 camera uses libcamera; capture with the `rpicam-*` tools (named
`libcamera-*` on older Raspberry Pi OS — detect which exists at startup).

- Stills for the agent: `rpicam-still -n -t 1 --width 1280 --height 720 -e jpg -o -`
  (stdout JPEG).
- Continuous frames for tracking: one long-running
  `rpicam-vid -n -t 0 --codec mjpeg --width 640 --height 480 --framerate 5 -o -`
  and split the MJPEG stream on JPEG SOI/EOI markers (`FF D8` … `FF D9`). Keep
  only the latest frame; drop the rest.
- One process owns the camera. Stills for the agent come from the same stream
  (latest frame) rather than starting `rpicam-still` while `rpicam-vid` runs.
- On the desktop (no Pi camera), fall back to `ffmpeg -f v4l2 -i /dev/video0`
  with the same MJPEG output, chosen by `FRITZ_CAMERA=rpicam|v4l2|off`.

## Presence + face following (SAM 3.1)

Loop at ~1–2 Hz (configurable `FRITZ_TRACK_HZ`, never above 2):

1. Take the latest frame (640×480 JPEG → base64 data URL).
2. SAM 3.1 with prompt `"person"` (try `"face"` too — pick whichever gives
   tighter boxes on real frames).
3. Parse boxes (see the parser warning in `meta-model-api`), pick the largest.
4. Map the box to gaze. The camera faces the user like the screen does, so a
   person on the left of the *image* stands on the device's right, which is
   screen-right from where the user sits:
   `gaze_x = (0.5 - cx) * 2`, `gaze_y = (cy - 0.4) * 2` (aim near eye level,
   not box center), clamp to [-1, 1]. Put the sign flips behind
   `FRITZ_GAZE_MIRROR_X/Y` and verify with a live test, since camera mounting
   or `--hflip` changes it.
5. Send `gaze x y` to the face. The face eases between updates.
6. Presence: present after 2 consecutive hits, absent after ~10 s of misses
   (debounce). On change, emit an event on a Go channel:
   `PresenceChanged{Present bool}`.

If SAM latency makes 1 Hz impossible, add a cheap local motion check first
(frame difference on a downscaled grayscale frame) and only call SAM when
something moved.

## Agent vision tools

Exposed to `fritz-agent`:

- `look(question string)` — latest frame + question → `muse-spark-1.3` image
  input → short answer. For "what am I holding?", "read this label", "does
  this look right?".
- `find_object(name string)` — SAM 3.1 with the object name → if found, send
  `gaze` toward it for ~3 s and return a rough location ("left side of the
  desk"). This is the "Fritz, where are my keys?" demo moment: the eyes turn
  to look at the object.

Vision must always drive an action or a conversation. A standalone "describe
this image" feature is on the hackathon's banned list (simple image
analyzers).

## Privacy

- Frames stay in memory. Never write them to disk except under
  `FRITZ_DEBUG_FRAMES=dir` for debugging, and never commit them.
- No face recognition or identity. "Person present" only.
- Log one line per presence change, not per frame.

## Testing

- Save a few real frames and raw SAM outputs under
  `api/internal/vision/testdata/` (scenery only, or with consent) and
  unit-test the parser and the box→gaze mapping.
- `go test ./internal/vision/...` must pass without a camera or network.
