#!/usr/bin/env bash
# Stitch the demo GIF: tray still (Ken-Burns) + two web-UI video segments.
#
# Inputs (produced by earlier pipeline steps):
#   /tmp/demo-tray/frame-01.png                  (Task 5 — cropped tray menu still)
#   /tmp/demo-webui/*1-servers*/video.webm       (Task 6 — server cards / federation)
#   /tmp/demo-webui/*2-tools*/video.webm         (Task 6 — tools / discovery)
#   /tmp/demo-webui/*3-activity*/video.webm      (Task 6 — activity log / audit)
#   /tmp/demo-webui/*4-security*/video.webm      (Task 6 — quarantine close-up)
# Outputs: docs/demo.gif + docs/demo.webp
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TRAY=/tmp/demo-tray/frame-01.png
WEB=/tmp/demo-webui
WORK=$(mktemp -d)
W=860; H=538; FPS=15; BG="#0f172a"; WEBSPEED=1.9   # speed up web segments to fit size budget

[ -f "$TRAY" ] || { echo "missing $TRAY (run Task 5)"; exit 1; }
# Ordered web segments (the leading number in the test name fixes the order).
# Portable array fill (avoid mapfile — macOS ships bash 3.2).
WEBS=()
for pat in 1-servers 2-tools 3-activity 4-security; do
  f=$(find "$WEB" -name '*.webm' -path "*$pat*" | head -1)
  [ -n "$f" ] && WEBS+=("$f")
done
[ "${#WEBS[@]}" -eq 4 ] || { echo "expected 4 web videos in $WEB, got ${#WEBS[@]} (run Task 6)"; exit 1; }

# Segment 0 — tray animation (~3.6s): roll the menu down, highlight the "Open Web UI"
# item, then a 1s shake (as if clicking it) before cutting to the web UI.
# Built in 3 phases (A roll, B settle+highlight, C shake) then concatenated.
MENU_H=470            # menu height on the canvas
HL_Y=264              # y of the "Open Web UI" row within the 470px-tall menu (calibrated)
ROLL=0.8; HOLD=1.8; SHAKE=1.0
SETTLE_Y=$(( (H - MENU_H) / 2 ))                 # final menu top (centered vertically)
ROLL_SPAN=$(( MENU_H + SETTLE_Y ))               # distance travelled during roll-down
ffmpeg -y -i "$TRAY" -vf "scale=-2:${MENU_H}" "$WORK/menu.png"
ffmpeg -y -f lavfi -i "color=c=${BG}:s=${W}x${H}" -frames:v 1 "$WORK/navy.png"
ffmpeg -y -i "$WORK/menu.png" -vf "drawbox=x=4:y=${HL_Y}:w=306:h=24:color=0x3b82f6@0.45:t=fill" "$WORK/menu_hl.png"
# A — roll down: menu slides from above to the settle position by t=ROLL-0.1
ffmpeg -y -loop 1 -i "$WORK/navy.png" -loop 1 -i "$WORK/menu.png" \
  -filter_complex "[0:v][1:v]overlay=x='(main_w-overlay_w)/2':y='-${MENU_H}+${ROLL_SPAN}*min(t/0.7\,1)'" \
  -r ${FPS} -t ${ROLL} -pix_fmt yuv420p -c:v libx264 "$WORK/segA.mp4"
# B — settle, then highlight "Open Web UI" at 0.7s in
ffmpeg -y -loop 1 -i "$WORK/navy.png" -loop 1 -i "$WORK/menu.png" -loop 1 -i "$WORK/menu_hl.png" \
  -filter_complex "[0:v][1:v]overlay=x='(main_w-overlay_w)/2':y=${SETTLE_Y}:enable='lt(t\,0.7)'[a];[a][2:v]overlay=x='(main_w-overlay_w)/2':y=${SETTLE_Y}:enable='gte(t\,0.7)'" \
  -r ${FPS} -t ${HOLD} -pix_fmt yuv420p -c:v libx264 "$WORK/segB.mp4"
# C — 1s shake (with highlight on)
ffmpeg -y -loop 1 -i "$WORK/navy.png" -loop 1 -i "$WORK/menu_hl.png" \
  -filter_complex "[0:v][1:v]overlay=x='(main_w-overlay_w)/2+5*sin(2*PI*t*9)':y='${SETTLE_Y}+3*sin(2*PI*t*8)'" \
  -r ${FPS} -t ${SHAKE} -pix_fmt yuv420p -c:v libx264 "$WORK/segC.mp4"
printf "file '%s'\nfile '%s'\nfile '%s'\n" "$WORK/segA.mp4" "$WORK/segB.mp4" "$WORK/segC.mp4" > "$WORK/traylist.txt"
ffmpeg -y -f concat -safe 0 -i "$WORK/traylist.txt" -c copy "$WORK/seg0.mp4"

# Segments 1..4 — web videos, scaled to canvas, sped up, no audio
i=1
for v in "${WEBS[@]}"; do
  ffmpeg -y -i "$v" -an \
    -vf "setpts=PTS/${WEBSPEED},scale=${W}:${H}:force_original_aspect_ratio=decrease,pad=${W}:${H}:(ow-iw)/2:(oh-ih)/2:color=${BG},fps=${FPS},format=yuv420p" \
    -c:v libx264 -pix_fmt yuv420p "$WORK/seg${i}.mp4"
  i=$((i+1))
done

# Concat with the concat FILTER (re-encodes — robust to timebase/SAR differences
# between the zoompan tray segment and the webm-derived web segments; the concat
# demuxer with -c copy silently drops mismatched segments).
ffmpeg -y -i "$WORK/seg0.mp4" -i "$WORK/seg1.mp4" -i "$WORK/seg2.mp4" -i "$WORK/seg3.mp4" -i "$WORK/seg4.mp4" \
  -filter_complex "[0:v]setsar=1,fps=${FPS}[a];[1:v]setsar=1,fps=${FPS}[b];[2:v]setsar=1,fps=${FPS}[c];[3:v]setsar=1,fps=${FPS}[d];[4:v]setsar=1,fps=${FPS}[e];[a][b][c][d][e]concat=n=5:v=1:a=0[v]" \
  -map "[v]" -c:v libx264 -pix_fmt yuv420p "$WORK/full.mp4"

# Palette-optimized GIF (-threads 1 dodges an ffmpeg 8.0 paletteuse threading bug)
ffmpeg -y -threads 1 -i "$WORK/full.mp4" -vf "fps=${FPS},scale=${W}:-2:flags=lanczos,palettegen=stats_mode=diff" "$WORK/pal.png"
ffmpeg -y -threads 1 -i "$WORK/full.mp4" -i "$WORK/pal.png" \
  -lavfi "fps=${FPS},scale=${W}:-2:flags=lanczos,paletteuse=dither=bayer:bayer_scale=3" "$ROOT/docs/demo.gif"

# WebP (smaller; also autoplays in README). Non-fatal — the GIF is the README embed.
ffmpeg -y -threads 1 -i "$WORK/full.mp4" -vcodec libwebp -filter:v "fps=${FPS},scale=${W}:-2" \
  -lossless 0 -compression_level 6 -q:v 55 -loop 0 -an "$ROOT/docs/demo.webp" || \
  { echo "WARN: webp encode failed (non-fatal); removing partial"; rm -f "$ROOT/docs/demo.webp"; }

echo "Wrote docs/demo.gif ($(du -h "$ROOT/docs/demo.gif" | cut -f1)) and docs/demo.webp ($(du -h "$ROOT/docs/demo.webp" | cut -f1))"
rm -rf "$WORK"
