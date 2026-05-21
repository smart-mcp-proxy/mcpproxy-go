#!/usr/bin/env bash
# Stitch the demo GIF: tray still (Ken-Burns) + two web-UI video segments.
#
# Inputs (produced by earlier pipeline steps):
#   /tmp/demo-tray/frame-01.png                         (Task 5 — cropped tray menu still)
#   /tmp/demo-webui/*dashboard*/video.webm              (Task 6 — server cards)
#   /tmp/demo-webui/*activity*/video.webm               (Task 6 — activity log)
# Outputs: docs/demo.gif + docs/demo.webp
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TRAY=/tmp/demo-tray/frame-01.png
WEB=/tmp/demo-webui
WORK=$(mktemp -d)
W=900; H=562; FPS=15; BG="#0f172a"; WEBSPEED=1.6   # speed up web segments to fit size budget

[ -f "$TRAY" ]   || { echo "missing $TRAY (run Task 5)"; exit 1; }
DASH=$(find "$WEB" -name '*.webm' -path '*dashboard*' | head -1)
ACT=$(find "$WEB" -name '*.webm' -path '*activity*' | head -1)
[ -n "$DASH" ] && [ -n "$ACT" ] || { echo "missing web videos in $WEB (run Task 6)"; exit 1; }

# Segment 0 — tray still: pad onto navy canvas, gentle Ken-Burns zoom (~3.5s)
ffmpeg -y -i "$TRAY" -vf "scale=-2:520,pad=${W}:${H}:(ow-iw)/2:(oh-ih)/2:color=${BG}" "$WORK/tray-canvas.png"
ffmpeg -y -loop 1 -i "$WORK/tray-canvas.png" -t 3.5 -r ${FPS} \
  -vf "zoompan=z='min(zoom+0.0009,1.10)':d=52:x='iw/2-(iw/zoom/2)':y='ih/2-(ih/zoom/2)':s=${W}x${H}:fps=${FPS},format=yuv420p" \
  -c:v libx264 -pix_fmt yuv420p "$WORK/seg0.mp4"

# Segments 1 & 2 — web videos, scaled to canvas, sped up, no audio
i=1
for v in "$DASH" "$ACT"; do
  ffmpeg -y -i "$v" -an \
    -vf "setpts=PTS/${WEBSPEED},scale=${W}:${H}:force_original_aspect_ratio=decrease,pad=${W}:${H}:(ow-iw)/2:(oh-ih)/2:color=${BG},fps=${FPS},format=yuv420p" \
    -c:v libx264 -pix_fmt yuv420p "$WORK/seg${i}.mp4"
  i=$((i+1))
done

# Concat with the concat FILTER (re-encodes — robust to timebase/SAR differences
# between the zoompan tray segment and the webm-derived web segments; the concat
# demuxer with -c copy silently drops mismatched segments).
ffmpeg -y -i "$WORK/seg0.mp4" -i "$WORK/seg1.mp4" -i "$WORK/seg2.mp4" \
  -filter_complex "[0:v]setsar=1,fps=${FPS}[a];[1:v]setsar=1,fps=${FPS}[b];[2:v]setsar=1,fps=${FPS}[c];[a][b][c]concat=n=3:v=1:a=0[v]" \
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
