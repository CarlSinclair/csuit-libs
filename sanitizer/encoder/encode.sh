#!/usr/bin/env bash
# Video sanitizer worker. The same script runs on a GitHub runner and on this
# host. It reads the source from a URL, probes it, transcodes in one ffmpeg pass
# (video rendition plus audio-only rendition), uploads each finished segment as
# soon as it exists and deletes it locally, then reports the outcome.
#
# Required environment: SOURCE_URL UPLOAD_BASE CALLBACK_URL TOKEN
# Optional: MAX_SECONDS (default 5400), THREADS (default 2), LOWER_PRIORITY=1,
# MODE=hls (default, 720p video HLS plus audio-only HLS) or mp4 (one sanitized out.mp4)
set -u

: "${SOURCE_URL:?}" "${UPLOAD_BASE:?}" "${CALLBACK_URL:?}" "${TOKEN:?}"
MAX_SECONDS="${MAX_SECONDS:-5400}"
THREADS="${THREADS:-2}"
MODE="${MODE:-hls}"
WORK="$(mktemp -d)"
FFPID=""
STATE="running"
MASTER="index.m3u8"
[ "$MODE" = "mp4" ] && MASTER="out.mp4"

prio=()
if [ "${LOWER_PRIORITY:-0}" = "1" ] && command -v nice >/dev/null 2>&1; then
  prio=(nice -n 19)
fi

report() {
  local ok="$1" msg="$2"
  curl -sS --fail --retry 5 --retry-delay 3 --max-time 60 -X POST \
    -H "Authorization: Bearer ${TOKEN}" -H "Content-Type: application/json" \
    --data "$(printf '{"ok":%s,"message":"%s","master":"%s"}' "$ok" "$(printf '%s' "$msg" | tr -d '"\\\n' | cut -c1-300)" "$MASTER")" \
    "${CALLBACK_URL}" >/dev/null \
    || echo "encoder: could not report outcome (ok=${ok}) to ${CALLBACK_URL}" >&2
}

cleanup() {
  [ -n "$FFPID" ] && kill "$FFPID" 2>/dev/null
  rm -rf "$WORK"
}
trap cleanup EXIT

fail() {
  STATE="failed"
  echo "encoder: $1" >&2
  report false "$1"
  exit 1
}

upload() {
  local f="$1" name url code
  name="$(basename "$f")"
  code="$(curl -sS --retry 4 --retry-delay 3 --max-time 60 -o "$WORK/.presign" -w '%{http_code}' \
    -H "Authorization: Bearer ${TOKEN}" "${UPLOAD_BASE}/presign/${name}")" || return 1
  if [ "$code" = "200" ]; then
    url="$(cat "$WORK/.presign")"
    curl -sS --fail --retry 4 --retry-delay 3 --max-time 900 -X PUT --data-binary "@${f}" "$url" >/dev/null
    return $?
  fi
  curl -sS --fail --retry 4 --retry-delay 3 --max-time 900 -X PUT \
    -H "Authorization: Bearer ${TOKEN}" --data-binary "@${f}" "${UPLOAD_BASE}/${name}" >/dev/null
}

drain() {
  local f
  for f in "$WORK"/*.ts; do
    [ -e "$f" ] || continue
    upload "$f" || return 1
    rm -f "$f"
  done
  return 0
}

probe="$("${prio[@]}" ffprobe -v error -protocol_whitelist http,https,tcp,tls,crypto -select_streams v:0 -show_entries stream=codec_type:format=duration \
  -of default=noprint_wrappers=1:nokey=1 "$SOURCE_URL" 2>&1)" || fail "the source could not be read as a media file"
set -- $probe
[ "${1:-}" = "video" ] || fail "the file has no video stream"
dur="${2:-0}"
awk -v d="$dur" -v m="$MAX_SECONDS" 'BEGIN{exit !(d+0 > m+0)}' && fail "the video is longer than the allowed ${MAX_SECONDS} seconds"

if [ "$MODE" = "mp4" ]; then
  "${prio[@]}" ffmpeg -nostdin -y -loglevel error -threads "$THREADS" -protocol_whitelist http,https,tcp,tls,crypto -i "$SOURCE_URL" \
    -map 0:v:0 -map '0:a:0?' -map_metadata -1 -map_chapters -1 -sn -dn -vf "scale=-2:'min(720,ih)'" \
    -c:v libx264 -preset veryfast -crf 24 -pix_fmt yuv420p -threads "$THREADS" -c:a aac -b:a 128k \
    -movflags +faststart "$WORK/out.mp4" || fail "ffmpeg failed to produce the mp4"
  [ -s "$WORK/out.mp4" ] || fail "ffmpeg did not produce out.mp4"
  upload "$WORK/out.mp4" || fail "uploading out.mp4 failed"
  report true "ok"
  STATE="done"
  exit 0
fi

"${prio[@]}" ffmpeg -nostdin -y -loglevel error -threads "$THREADS" -protocol_whitelist http,https,tcp,tls,crypto -i "$SOURCE_URL" \
  -map 0:v:0 -map '0:a:0?' -vf "scale=-2:'min(720,ih)'" -c:v libx264 -preset veryfast -crf 23 -threads "$THREADS" -c:a aac -b:a 128k \
  -f hls -hls_time 6 -hls_playlist_type vod -hls_flags +temp_file \
  -hls_segment_filename "$WORK/seg%04d.ts" "$WORK/index.m3u8" \
  -map '0:a:0?' -vn -c:a aac -b:a 96k \
  -f hls -hls_time 6 -hls_playlist_type vod -hls_flags +temp_file \
  -hls_segment_filename "$WORK/aud%04d.ts" "$WORK/audio.m3u8" &
FFPID=$!

while kill -0 "$FFPID" 2>/dev/null; do
  drain || fail "uploading a finished segment failed"
  sleep 2
done
wait "$FFPID"
rc=$?
FFPID=""
[ "$rc" -eq 0 ] || fail "ffmpeg failed with exit code ${rc}"

drain || fail "uploading the last segments failed"
for p in index.m3u8 audio.m3u8; do
  [ -s "$WORK/$p" ] || fail "ffmpeg did not produce $p"
  upload "$WORK/$p" || fail "uploading $p failed"
done
report true "ok"
STATE="done"
