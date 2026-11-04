"""Fritz's original printable body. (The final Fritz in the README photo
was printed by an outside 3D printing service, which replaced this round
base with a square one; the head and lid follow this design.)

A pod base that hides a standard-size servo
(Hitec HS-422; an MG995 or MG996R fits the same base), and a head that
holds the Raspberry Pi + 3.5" TFT and turns on top of it.

    python build_body.py            # writes stl/*.stl
    python build_body.py --json X   # also writes a mesh bundle for the web preview

Needs: pip install manifold3d numpy

Coordinates: millimetres, z up, the servo's output shaft on the z axis,
the face (screen) looking toward -y.

Parts (print each flat side down, PLA, 0.2 mm layers, 15-20 % infill):
  base_shell  pod body with the servo deck on top   print deck-down
  base_floor  press-fit floor with rubber-foot pockets
  neck        collar that the servo's round horn screws into
  head        bucket that holds the Pi + screen      print floor-down
  lid         rounded top with ears and vents        print flat side down

Everything that depends on your parts is in MEASURE below. Measure
your Pi + screen ("slab") with calipers before printing: it is the
one thing that differs between builds (bare stack vs. official case).
"""

import argparse
import base64
import json
import math
import struct
from pathlib import Path

import numpy as np
from manifold3d import CrossSection, Error, JoinType, Manifold

SEG = 96  # circle smoothness

# --- MEASURE ---------------------------------------------------------------
# The Pi + TFT as one block, screen facing forward, landscape. Defaults
# fit a Pi 4B/Pi 5 in a case base with a 3.5" GPIO TFT on top;
# a bare Pi + TFT stack is smaller (~86 x 57 x 25): pad it with foam
# tape or change these.
SLAB_W = 96.0   # left-right
SLAB_H = 65.0   # bottom-top
SLAB_D = 34.0   # front (glass) to back
FIT = 0.6       # clearance around the slab
# The screen's visible area and where its centre sits relative to the
# slab's centre (+x right, +z up, seen from the front).
WIN_W, WIN_H = 76.0, 52.0
WIN_DX, WIN_DZ = 0.0, 0.0
# Which side the Pi's USB-A / Ethernet ports face: +1 right, -1 left.
PORT_SIDE = +1
# Height of the ledges the slab rests on: room underneath for a
# right-angle USB-C plug (power) to turn toward the back.
LEDGE_H = 14.0
# Cable space behind the slab: the servo's jumper wires (from pins 32
# and 34 at the top edge) run down it into the channel under the slab,
# and everything leaves through the opening low on the back.
BACK_GAP = 8.0

# Hitec HS-422 (standard size, 40.6 x 19.8 x 36.6). The deck cut-out
# also takes an MG995/MG996R (40.7 x 19.7), and the tab holes are
# slots spanning both pitches (makers' figures disagree), so the base
# never needs reprinting for a servo swap. Only the neck depends on
# the servo: see HORN_TOP_ABOVE_TABS.
SERVO_L, SERVO_W = 40.7, 19.8
SERVO_TAB_Z = 26.5       # body bottom -> underside of the mounting tabs
SERVO_TAB_T = 2.5        # tab thickness
SERVO_HOLE_PITCH_L = (47.6, 50.0)   # slot: HS-422 ~48, MG996R ~49.5
SERVO_HOLE_PITCH_W = 10.0
SERVO_SHAFT_FROM_END = 10.0   # shaft centre -> nearest end of the body
# With the round horn fitted: top of the tabs -> top of the horn disc.
# MEASURE THIS on your servo before printing the neck. 13 is set low
# on purpose for an unmeasured HS-422 (estimate ~15): too low only
# widens the neck-to-deck gap (felt pads fill it), too high and the
# neck's rim hits the deck. MG996R/MG995: about 17.
HORN_TOP_ABOVE_TABS = 13.0
HORN_D, HORN_T = 25.5, 2.6    # round horn: pocket fits Hitec and MG discs
# --------------------------------------------------------------------------

WALL = 3.0
BASE_H = 48.0         # pod height (deck top)
BASE_RB, BASE_RT = 60.0, 52.0   # pod radius at the bottom / top
BASE_CHAMFER = 6.0
DECK_T = 3.0
NECK_R = 44.0         # collar radius; clears the servo tabs and bolts
NECK_PLATE_T = 6.0
NECK_GAP = 1.2        # collar rim -> deck: anti-tip, not a bearing
HEAD_R = 8.0          # outer corner radius of the head
POCKET_R = 3.0        # inner corner radius (slab corners)
FLOOR_T = 3.0
CAP_H = 12.0          # lid height above the slab
TONGUE_H, TONGUE_T, TONGUE_FIT = 3.0, 1.2, 0.25
M3 = 3.4              # M3 clearance hole
NECK_BOLTS = [(sx * 16.0, sy * 11.0) for sx in (-1, 1) for sy in (-1, 1)]

# Derived layout.
BODY_CX = SERVO_L / 2 - SERVO_SHAFT_FROM_END   # servo body centre, shaft at 0
HORN_TOP_Z = BASE_H + SERVO_TAB_T + HORN_TOP_ABOVE_TABS
NECK_Z0 = HORN_TOP_Z - HORN_T                  # neck plate underside
HEAD_Z0 = NECK_Z0 + NECK_PLATE_T               # head floor underside
POCKET_W = SLAB_W + 2 * FIT
POCKET_D = SLAB_D + 2 * FIT + BACK_GAP
SLAB_Y0 = -POCKET_D / 2 + FIT                  # slab front (glass), against the window
SLAB_Y1 = SLAB_Y0 + SLAB_D                     # slab back; the cable gap is behind
POCKET_Z0 = HEAD_Z0 + FLOOR_T                  # pocket floor (ledges stand here)
SLAB_Z0 = POCKET_Z0 + LEDGE_H                  # slab bottom
SPLIT_Z = SLAB_Z0 + SLAB_H + FIT               # bucket / lid split
HEAD_W = POCKET_W + 2 * WALL
HEAD_D = POCKET_D + 2 * WALL
HEAD_TOP = SPLIT_Z + CAP_H


def box(x0, x1, y0, y1, z0, z1):
    return Manifold.cube((x1 - x0, y1 - y0, z1 - z0)).translate((x0, y0, z0))


def cyl(r, z0, z1, x=0.0, y=0.0, seg=SEG):
    return Manifold.cylinder(z1 - z0, r, r, seg).translate((x, y, z0))


def rbox(w, d, z0, z1, r, top_round=True):
    """Box centred on the z axis with rounded vertical edges and, when
    top_round, rounded top edges (radius r)."""
    pts = [(sx * (w / 2 - r), sy * (d / 2 - r)) for sx in (-1, 1) for sy in (-1, 1)]
    parts = []
    for x, y in pts:
        if top_round:
            parts.append(cyl(r, z0, z1 - r, x, y, 48))
            parts.append(Manifold.sphere(r, 48).translate((x, y, z1 - r)))
        else:
            parts.append(cyl(r, z0, z1, x, y, 48))
    return Manifold.batch_hull(parts)


def rrect_y(w, h, r, y0, y1, cx=0.0, cz=0.0):
    """Rounded rectangle in the x-z plane, extruded along y (a window)."""
    xs = CrossSection.square((w - 2 * r, h - 2 * r), True).offset(r, JoinType.Round, 2, 48)
    m = Manifold.extrude(xs, y1 - y0).rotate((-90, 0, 0))  # extrude along +y
    return m.translate((cx, y0, cz))


def revolve(profile):
    return Manifold.revolve(CrossSection([profile]), SEG)


# --- base -------------------------------------------------------------------

def base_shell():
    rb, rt, ch, t = BASE_RB, BASE_RT, BASE_CHAMFER, WALL
    outer = revolve([(0, 0), (rb, 0), (rt, BASE_H - ch), (rt - ch, BASE_H), (0, BASE_H)])
    inner = revolve([(0, -1), (rb - t, -1), (rt - t, BASE_H - ch),
                     (rt - ch - 0.4 * t, BASE_H - DECK_T), (0, BASE_H - DECK_T)])
    shell = outer - inner
    # Servo drops in from above through the deck and hangs by its tabs.
    cut = box(BODY_CX - SERVO_L / 2 - 0.4, BODY_CX + SERVO_L / 2 + 0.4,
              -SERVO_W / 2 - 0.4, SERVO_W / 2 + 0.4, BASE_H - DECK_T - 1, BASE_H + 1)
    shell -= cut
    for sx in (-1, 1):
        for sy in (-1, 1):
            ends = [cyl(M3 / 2, BASE_H - DECK_T - 1, BASE_H + 1,
                        BODY_CX + sx * pitch / 2, sy * SERVO_HOLE_PITCH_W / 2, 32)
                    for pitch in SERVO_HOLE_PITCH_L]
            shell -= Manifold.batch_hull(ends)
    # Wires out the back: servo leads up to the Pi, servo power in.
    shell -= rrect_y(18, 12, 3, 0, rb + 5, 0, 16)
    return shell


def base_floor():
    r = BASE_RB - WALL - 0.3
    floor = cyl(r, 0, 3)
    # Press-fit lip, just inside the wall.
    floor += cyl(r - 0.2, 3, 7) - cyl(r - 2.2, 2.9, 7.1)
    # Pockets for 4 stick-on rubber feet (10 mm), on the underside.
    for a in (45, 135, 225, 315):
        x, y = (r - 12) * math.cos(math.radians(a)), (r - 12) * math.sin(math.radians(a))
        floor -= cyl(5.4, -1, 1.2, x, y, 48)
    # Leave room for the wire hole above.
    floor -= box(-10, 10, r - 6, r + 2, 2.9, 7.1)
    return floor


# --- turning parts -------------------------------------------------------------

def neck():
    plate = cyl(NECK_R, NECK_Z0, NECK_Z0 + NECK_PLATE_T)
    skirt = cyl(NECK_R, BASE_H + NECK_GAP, NECK_Z0 + 0.01) - cyl(NECK_R - 3, BASE_H + NECK_GAP - 1, NECK_Z0 + 0.02)
    m = plate + skirt
    # The round horn sits in a pocket on the underside; screw it in
    # with the small screws from the servo bag.
    m -= cyl(HORN_D / 2 + 0.3, NECK_Z0 - 1, NECK_Z0 + HORN_T)
    # Screwdriver access to the horn's centre screw (through the head floor).
    m -= cyl(3.5, NECK_Z0 - 1, NECK_Z0 + NECK_PLATE_T + 1)
    for x, y in NECK_BOLTS:
        m -= cyl(M3 / 2, NECK_Z0 - 1, NECK_Z0 + NECK_PLATE_T + 1, x, y, 32)
    return m


def head_outer():
    return rbox(HEAD_W, HEAD_D, HEAD_Z0, HEAD_TOP, HEAD_R)


def pocket(grow=0.0, z0=POCKET_Z0, z1=HEAD_TOP - WALL, flat=False):
    return rbox(POCKET_W + 2 * grow, POCKET_D + 2 * grow, z0, z1, POCKET_R + grow, top_round=not flat)


def head():
    outer = head_outer() ^ box(-200, 200, -200, 200, HEAD_Z0, SPLIT_Z)
    m = outer - pocket(z1=SPLIT_Z + 1, flat=True)
    # Tongue on the inner side of the rim; the lid slides over it.
    m += (pocket(TONGUE_T, SPLIT_Z - 0.01, SPLIT_Z + TONGUE_H, flat=True)
          - pocket(0, SPLIT_Z - 1, SPLIT_Z + TONGUE_H + 1, flat=True))
    # Ledges the slab sits on; the gap between them is a cable channel
    # (USB-C plug) that joins the cable gap behind the slab and opens
    # out the back. Two ribs behind the slab's outer edges stop it
    # sliding back into the gap; cables pass between them.
    for sx in (-1, 1):
        x_in = sx * (POCKET_W / 2 - 10)
        x_out = sx * (POCKET_W / 2 + 0.01)
        m += box(min(x_in, x_out), max(x_in, x_out), -POCKET_D / 2, SLAB_Y1 + FIT, POCKET_Z0 - 0.01, SLAB_Z0)
        m += box(min(x_in, x_out), max(x_in, x_out), SLAB_Y1 + FIT, SLAB_Y1 + FIT + 2.5,
                 POCKET_Z0 - 0.01, SLAB_Z0 + SLAB_H - 12)
    m -= box(-POCKET_W / 2 + 10, POCKET_W / 2 - 10, 0, HEAD_D, POCKET_Z0, SLAB_Z0 - 2)
    # Face: the screen window, with a shallow bezel step.
    wz = SLAB_Z0 + SLAB_H / 2 + WIN_DZ
    m -= rrect_y(WIN_W, WIN_H, 4, -HEAD_D, 0, WIN_DX, wz)
    m -= rrect_y(WIN_W + 6, WIN_H + 6, 7, -HEAD_D, -HEAD_D / 2 + 0.8, WIN_DX, wz)
    # Side window for the Pi's USB-A (camera) and Ethernet.
    sx = PORT_SIDE * (HEAD_W / 2)
    port = box(min(sx, 0) - 1 if PORT_SIDE < 0 else POCKET_W / 2 - 1,
               max(sx, 0) + 1 if PORT_SIDE > 0 else -POCKET_W / 2 + 1,
               SLAB_Y0 + 8, SLAB_Y1 - 2, SLAB_Z0 + 5, SLAB_Z0 + SLAB_H - 5)
    m -= port
    # Vents low on the back (the Pi runs warm).
    for i in range(-3, 4):
        m -= box(i * 9 - 2, i * 9 + 2, POCKET_D / 2 - 1, HEAD_D, SLAB_Z0 + 12, SLAB_Z0 + SLAB_H - 10)
    # Head floor: centre access hole and the four neck bolts.
    m -= cyl(3.5, HEAD_Z0 - 1, POCKET_Z0 + 1)
    for x, y in NECK_BOLTS:
        m -= cyl(M3 / 2, HEAD_Z0 - 1, POCKET_Z0 + 1, x, y, 32)
    return m


def lid():
    cap = head_outer() ^ box(-200, 200, -200, 200, SPLIT_Z, HEAD_TOP + 50)
    cap -= pocket(z0=SPLIT_Z - 1)
    cap -= pocket(TONGUE_T + TONGUE_FIT, SPLIT_Z - 1, SPLIT_Z + TONGUE_H + TONGUE_FIT, flat=True)
    # Ears: two round bumps, the "cute" part.
    for sx in (-1, 1):
        ear = Manifold.sphere(11, 64).scale((1, 0.7, 1)).translate((sx * (HEAD_W / 2 - 20), 0, HEAD_TOP - 4))
        cap += ear ^ box(-200, 200, -200, 200, HEAD_TOP - 6, HEAD_TOP + 50)
    # Vent slots across the top.
    for i in range(-2, 3):
        cap -= box(i * 8 - 1.6, i * 8 + 1.6, -POCKET_D / 2 + 6, POCKET_D / 2 - 6, HEAD_TOP - WALL - 1, HEAD_TOP + 1)
    return cap


PARTS = {
    "base_shell": base_shell,
    "base_floor": base_floor,
    "neck": neck,
    "head": head,
    "lid": lid,
}

# Print orientation: rotate so the flat face is on the bed, then drop to z=0.
FLIP = {"base_shell": True}


def write_stl(mesh: Manifold, path: Path):
    m = mesh.to_mesh()
    v = np.asarray(m.vert_properties)[:, :3].astype(np.float32)
    t = np.asarray(m.tri_verts)
    tri = v[t]
    n = np.cross(tri[:, 1] - tri[:, 0], tri[:, 2] - tri[:, 0])
    n /= np.maximum(np.linalg.norm(n, axis=1, keepdims=True), 1e-12)
    with open(path, "wb") as f:
        f.write(b"Fritz body - generated by build_body.py".ljust(80, b" "))
        f.write(struct.pack("<I", len(t)))
        rec = np.zeros(len(t), dtype=[("n", "<f4", 3), ("v", "<f4", (3, 3)), ("a", "<u2")])
        rec["n"], rec["v"] = n, tri
        f.write(rec.tobytes())


def for_print(name, part: Manifold) -> Manifold:
    if FLIP.get(name):
        part = part.rotate((180, 0, 0))
    lo = part.bounding_box()[2]
    return part.translate((0, 0, -lo))


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--out", default=str(Path(__file__).parent / "stl"))
    ap.add_argument("--json", help="also write an assembled mesh bundle (web preview)")
    args = ap.parse_args()
    out = Path(args.out)
    out.mkdir(parents=True, exist_ok=True)
    bundle = {}
    for name, build in PARTS.items():
        part = build()
        if part.status() != Error.NoError or part.is_empty():
            raise SystemExit(f"{name}: bad geometry ({part.status()})")
        printed = for_print(name, part)
        write_stl(printed, out / f"{name}.stl")
        bb = printed.bounding_box()
        print(f"{name:11s} {bb[3]-bb[0]:6.1f} x {bb[4]-bb[1]:6.1f} x {bb[5]-bb[2]:6.1f} mm  "
              f"{part.volume()/1000:6.1f} cm3  genus {part.genus()}")
        if args.json:
            m = part.to_mesh()
            v = np.asarray(m.vert_properties)[:, :3].astype(np.float32)
            t = np.asarray(m.tri_verts).astype(np.uint32)
            bundle[name] = {"v": base64.b64encode(v.tobytes()).decode(),
                            "i": base64.b64encode(t.tobytes()).decode()}
    if args.json:
        bundle["_layout"] = {"base_h": BASE_H, "head_z0": HEAD_Z0, "neck_z0": NECK_Z0,
                             "slab": [SLAB_W, SLAB_H, SLAB_D], "slab_z0": SLAB_Z0,
                             "slab_y0": SLAB_Y0, "pocket_z0": POCKET_Z0, "head_d": HEAD_D,
                             "head_w": HEAD_W, "base_rb": BASE_RB, "port_side": PORT_SIDE,
                             "servo": {"cx": BODY_CX, "l": SERVO_L, "w": SERVO_W,
                                       "z0": BASE_H - SERVO_TAB_Z, "h": SERVO_TAB_Z + SERVO_TAB_T + 10.4},
                             "top": HEAD_TOP}
        Path(args.json).write_text(json.dumps(bundle))
    print(f"height assembled: {HEAD_TOP:.0f} mm, head {HEAD_W:.0f} x {HEAD_D:.0f} mm, base Ø{2*BASE_RB:.0f} mm")


if __name__ == "__main__":
    main()
