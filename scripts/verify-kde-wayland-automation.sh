#!/usr/bin/env bash
# Real KDE Wayland native CLI acceptance using a private KWin compositor.
set -euo pipefail
if [[ "$(printenv PF_KDE_PRIVATE_SESSION || echo 0)" != 1 ]]; then
  exec dbus-run-session -- env PF_KDE_PRIVATE_SESSION=1 "$0"
fi
PF_BIN=$PF_KDE_CLI_BINARY
[[ "$PF_BIN" = /* && -x "$PF_BIN" ]] || { echo "PF_KDE_CLI_BINARY needs an executable absolute path" >&2; exit 2; }
for tool in kwin_wayland foot python3 busctl; do command -v "$tool" >/dev/null || exit 2; done
workdir=$(mktemp -d /var/tmp/pf-kde-e2e.XXXXXX)
runtime=$(mktemp -d /tmp/pf-kde.XXXXXX)
chmod 700 "$runtime"
kwin_pid=""
foot_pid=""
cleanup() {
  rc=$?
  if ((rc != 0)); then
    echo "KDE acceptance failed, rc=$rc" >&2
    tail -25 "$workdir/kwin.log" >&2 || :
    tail -15 "$workdir/foot.log" >&2 || :
    cat "$workdir/error" >&2 || :
  fi
  if [[ -n "$foot_pid" ]]; then kill "$foot_pid" 2>/dev/null || :; wait "$foot_pid" 2>/dev/null || :; fi
  if [[ -n "$kwin_pid" ]]; then kill "$kwin_pid" 2>/dev/null || :; wait "$kwin_pid" 2>/dev/null || :; fi
  rm -rf -- "$workdir" "$runtime"
}
trap cleanup EXIT

export XDG_RUNTIME_DIR="$runtime" XDG_CURRENT_DESKTOP=KDE XDG_SESSION_TYPE=wayland
export WAYLAND_DISPLAY=wayland-pf-acceptance
export XDG_CONFIG_HOME="$workdir/config" XDG_DATA_HOME="$workdir/data" XDG_CACHE_HOME="$workdir/cache"
mkdir -p "$XDG_CONFIG_HOME" "$XDG_DATA_HOME" "$XDG_CACHE_HOME"
unset DISPLAY XAUTHORITY
QT_QPA_PLATFORM=offscreen kwin_wayland --virtual --no-lockscreen --socket "$WAYLAND_DISPLAY" --width 1024 --height 768 > "$workdir/kwin.log" 2>&1 &
kwin_pid=$!
for _ in $(seq 1 100); do
  test -S "$runtime/$WAYLAND_DISPLAY" && break
  kill -0 "$kwin_pid" 2>/dev/null || exit 3
  sleep .15
done
test -S "$runtime/$WAYLAND_DISPLAY"
foot --app-id=pf-kde-acceptance --title=pf-kde-acceptance -e sh -c 'sleep 90' > "$workdir/foot.log" 2>&1 &
foot_pid=$!

state() { "$PF_BIN" window list --output=json > "$workdir/windows.json" 2> "$workdir/error"; }
expect_state() {
  local key=$1 want=$2
  for _ in $(seq 1 60); do
    if state && python3 -c '
import json,sys
rows=json.load(open(sys.argv[1]))
matches=[w for w in rows if w["Title"]=="pf-kde-acceptance"]
expected={"true":True,"false":False}.get(sys.argv[3],sys.argv[3])
assert len(matches)==1 and matches[0].get(sys.argv[2])==expected
' "$workdir/windows.json" "$key" "$want" 2>/dev/null; then return; fi
    sleep .15
  done
  echo "Window never attained $key=$want" >&2
  cat "$workdir/windows.json" >&2 || :
  exit 1
}

expect_state Title pf-kde-acceptance
"$PF_BIN" info --output=json > "$workdir/info.json"
python3 -c '
import json,sys
r=json.load(open(sys.argv[1]))
assert "KDE" in r["compositor"] and "Wayland" in r["compositor"]
assert r["capabilities"]["windows"]["supported"]
' "$workdir/info.json"
echo "PASS: genuine KDE Wayland window discovery and capabilities"

"$PF_BIN" window minimize pf-kde-acceptance
expect_state Minimized true
"$PF_BIN" window activate pf-kde-acceptance --verify --verify-timeout 5s
expect_state Active true
expect_state Minimized false
echo "PASS: true minimize, restore and verified focus"

"$PF_BIN" window keep-below pf-kde-acceptance on
expect_state KeepBelow true
"$PF_BIN" window keep-below pf-kde-acceptance off
expect_state KeepBelow false
"$PF_BIN" window keep-above pf-kde-acceptance on
expect_state KeepAbove true
"$PF_BIN" window keep-above pf-kde-acceptance off
expect_state KeepAbove false
"$PF_BIN" window fullscreen pf-kde-acceptance
expect_state Fullscreen true
"$PF_BIN" window unfullscreen pf-kde-acceptance
expect_state Fullscreen false
echo "PASS: keep-above/below, fullscreen and unfullscreen"

if python3 -c 'import json,sys;w=json.load(open(sys.argv[1]))[0];assert not w["Shadeable"]' "$workdir/windows.json"; then
  if "$PF_BIN" window shade pf-kde-acceptance on > "$workdir/shade-out" 2> "$workdir/shade-err"; then
    echo "Disallowed native Wayland shading falsely succeeded" >&2; exit 1
  fi
  grep -q 'not shadeable' "$workdir/shade-err"
else
  "$PF_BIN" window shade pf-kde-acceptance on
  expect_state Shaded true
  "$PF_BIN" window shade pf-kde-acceptance off
  expect_state Shaded false
fi
echo "PASS: shadeability correctly enforced"

# Demonstrate native custom tiling and undo against the real compositor. A
# geometry-only emulation must never count as a successful tile assignment.
"$PF_BIN" window tile pf-kde-acceptance --x 256 --y 256
expect_state Tiled true
"$PF_BIN" window float pf-kde-acceptance
expect_state Tiled false
echo "PASS: real KWin custom tile assignment and floating restore"

"$PF_BIN" desktop list --output=json > "$workdir/desktops.json"
busctl --user call org.kde.KWin /VirtualDesktopManager org.kde.KWin.VirtualDesktopManager createDesktop us 1 "PF Acceptance"
"$PF_BIN" desktop list --output=json > "$workdir/desktops.json"
new_id=$(python3 -c 'import json,sys;d=json.load(open(sys.argv[1]));ids=[x["id"] for x in d if x["name"]=="PF Acceptance"];assert len(ids)==1;print(ids[0])' "$workdir/desktops.json")
"$PF_BIN" window set-desktop pf-kde-acceptance "$new_id"
expect_state DesktopIDs "$new_id"
"$PF_BIN" desktop switch "$new_id"
"$PF_BIN" desktop list --output=json > "$workdir/desktops.json"
python3 -c 'import json,sys;d=json.load(open(sys.argv[1]));assert any(x["id"]==sys.argv[2] and x["current"] for x in d)' "$workdir/desktops.json" "$new_id"
"$PF_BIN" window activate pf-kde-acceptance --verify --verify-timeout 5s
echo "PASS: virtual desktop creation, assignment, switch and focus"

# Invalid desktop IDs must fail without changing the current desktop or the
# assigned desktop of the real window.
bad_id=perfuncted-nonexistent-desktop-uuid
if "$PF_BIN" desktop switch "$bad_id" > "$workdir/bad-switch" 2> "$workdir/bad-switch-err"; then
  echo "Unknown desktop falsely accepted" >&2; exit 1
fi
if "$PF_BIN" window set-desktop pf-kde-acceptance "$bad_id" > "$workdir/bad-assign" 2> "$workdir/bad-assign-err"; then
  echo "Window moved to unknown desktop" >&2; exit 1
fi
expect_state DesktopIDs "$new_id"
"$PF_BIN" desktop list --output=json > "$workdir/desktops.json"
python3 -c 'import json,sys;d=json.load(open(sys.argv[1]));assert any(x["id"]==sys.argv[2] and x["current"] for x in d)' "$workdir/desktops.json" "$new_id"
echo "PASS: invalid desktop IDs cannot alter window placement or active desktop"

cursor=$("$PF_BIN" window cursor)
[[ "$cursor" =~ ^-?[0-9]+,-?[0-9]+$ ]] || exit 1
if python3 -c 'import json,sys;r=json.load(open(sys.argv[1]));assert r["capabilities"]["input"]["supported"]' "$workdir/info.json" 2>/dev/null; then
  input_cursor=$("$PF_BIN" input location)
  [[ "$cursor" == "$input_cursor" ]] || { echo "KWin cursor vs input location mismatch" >&2; exit 1; }
  if "$PF_BIN" input move-relative --dx 6 --dy 4 > "$workdir/move-out" 2> "$workdir/move-err"; then
    [[ "$cursor" != "$("$PF_BIN" window cursor)" ]] || { echo "Unverified pointer motion falsely succeeded" >&2; exit 1; }
  fi
fi
echo "PASS: compositor-backed cursor reading and verified-only relative input"
echo "KDE WAYLAND ACCEPTANCE PASSED"
