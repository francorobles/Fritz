---
name: fritz-vision
description: >
  Fritz's camera: capturing frames from the C922 USB webcam, detecting
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

The camera is a **Logitech C922 USB webcam** — load `fritz-c922` for the
device path, the exact ffmpeg command, and camera controls. In short:

- One long-running `ffmpeg` reads the C922's native MJPEG at 1280×720 and
  copies frames to stdout without re-encoding; Go splits them on JPEG
  markers and keeps only the latest frame.
- One process owns the camera. Tracking, `look`, and `find_object` all read
  that shared latest frame.
- `FRITZ_CAMERA=v4l2|off` (default `v4l2`). The same code works on a Linux
  laptop with any UVC webcam.

## Presence + face following (SAM 3.1)

Loop at ~1–2 Hz (configurable `FRITZ_TRACK_HZ`, never above 2):

1. Take the latest frame, downscale to 640 px wide (see `fritz-c922`),
   base64 data URL.
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
- `find_object(name string)` — **built** (`vision/objects.go` Finder,
  `agent/vision.go` tool). SAM 3.1 with the object name on the latest frame;
  if found, `Tracker.Look` holds the eyes on it for 4 s (overriding face
  following) and the tool returns where it is *from the user's seat*
  (`Describe`: image-left = user's right, lower = front of the desk;
  honors `FRITZ_GAZE_MIRROR_X`). This is the "Fritz, where are my keys?"
  demo moment.
- **Last-seen memory** — the Finder also scans a watch list in the
  background, one object every 10 s (`FRITZ_WATCH_OBJECTS`, default
  `keys,wallet,glasses,phone`; `off` disables). Each sighting (place,
  seen time, and the time it went missing) is saved in the memory store
  (`sightings` table), so after the keys leave the frame — or after a
  restart — Fritz answers "I last saw them at 3:40 on your right; they
  were gone by 3:52". Only the place text and times are stored, never
  frames.
- Manual test: `go run ./cmd/visioncheck -find keys` (stop voicecheck
  first — one process owns the camera).

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
