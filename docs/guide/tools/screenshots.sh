#!/usr/bin/env bash
# Regenerates docs/guide/img/*.png: a throwaway sshd (ForceCommand, never a login shell),
# fake RAW/IRC/GitHub/VNC servers, a demo data directory, then one headless app run per shot.
# Needs: xvfb-run, sshd, ssh-keygen, python3, ImageMagick. Usage: docs/guide/tools/screenshots.sh [shot...]
set -euo pipefail
cd "$(dirname "$0")/.."
GUIDE=$PWD
B=${NEXUS_GUIDE_TMP:-/tmp/nexus-guide}
IMG=$GUIDE/img
APP=$GUIDE/../../rust/target/debug/nexus-terminal
SCALE=${SCALE:-2}
mkdir -p "$IMG"
rm -rf "$B" && mkdir -p "$B/home/.ssh" "$B/sshd"

(cd ../../rust && cargo build -q -p nexus && cargo build -q -p nexus-core --example demo_data)

# Demo home: what the SFTP browser, the shell and the SSH key manager see.
H=$B/home
mkdir -p "$H"/{projects/nexus,backups,logs,www/static}
printf 'server {\n  listen 443 ssl;\n}\n' > "$H/nginx.conf"
echo '#!/bin/sh' > "$H/deploy.sh"; chmod +x "$H/deploy.sh"
head -c 180000 /dev/urandom > "$H/backups/db-2026-10-10.sql.gz"
head -c 52000 /dev/urandom > "$H/logs/access.log"
echo "# Notes" > "$H/README.md"
ssh-keygen -q -t ed25519 -N '' -C 'andy@laptop' -f "$H/.ssh/id_ed25519"
ssh-keygen -q -t rsa -b 2048 -N '' -C 'andy@work' -f "$H/.ssh/work_rsa"
ssh-keygen -q -t ecdsa -N '' -C 'deploy-bot' -f "$H/.ssh/deploy_ecdsa"

# sshd on 42022 (ForceCommand keeps it away from ~/.profile).
S=$B/sshd
ssh-keygen -q -t ed25519 -N '' -f "$S/host"
cp "$H/.ssh/id_ed25519.pub" "$S/authorized_keys"
SFTP=$(ls /usr/lib/openssh/sftp-server /usr/libexec/openssh/sftp-server /usr/libexec/sftp-server 2>/dev/null | head -1 || true)
cat > "$S/config" <<CFG
Port 42022
ListenAddress 127.0.0.1
HostKey $S/host
AuthorizedKeysFile $S/authorized_keys
PidFile $S/pid
StrictModes no
UsePAM no
PasswordAuthentication no
KbdInteractiveAuthentication no
PubkeyAuthentication yes
PrintMotd no
PrintLastLog no
Subsystem sftp $SFTP
ForceCommand $GUIDE/tools/demo-shell.sh $H $SFTP
CFG
"$(command -v sshd)" -f "$S/config" -E "$S/log"
python3 -I "$GUIDE/tools/fakes.py" > "$B/fakes.log" 2>&1 &
FAKES=$!
trap 'kill $FAKES 2>/dev/null; kill "$(cat "$S/pid" 2>/dev/null)" 2>/dev/null || true' EXIT
until grep -q ready "$B/fakes.log"; do sleep 0.2; done

"$(dirname "$APP")/examples/demo_data" "$B/data" "$H/.ssh/id_ed25519" > /dev/null
cp -r "$B/data" "$B/data.clean"

shot() { # name, then app args
  local name=$1; shift
  if [ ${#ONLY[@]} -gt 0 ] && [[ ! " ${ONLY[*]} " =~ " $name " ]]; then return 0; fi
  rm -rf "$B/data" && cp -r "$B/data.clean" "$B/data"
  HOME=$H WINIT_X11_SCALE_FACTOR=$SCALE timeout 60 xvfb-run -a -s "-screen 0 3200x2000x24" \
    "$APP" --data-dir "$B/data" --size 1360x860 --screenshot "$IMG/$name.png" "$@" 2>> "$B/app.log" || echo "  ! $name failed"
  # 256-colour PNGs are a quarter of the size and look identical for UI shots.
  [ -f "$IMG/$name.png" ] && magick "$IMG/$name.png" -colors 256 "PNG8:$IMG/$name.png"
  echo "  $name"
}
ONLY=("$@")

shot welcome
shot welcome-midnight --theme midnight
shot welcome-light    --theme light
shot ssh              --connect "Sensor feed" --connect "Libera Chat" --connect atlas --shot-delay 5500
shot ssh-light        --theme light --connect atlas --shot-delay 5500
shot hostkey          --connect hello-ssh --shot-delay 5000
shot split            --connect atlas --do "Split Pane Right" --shot-delay 5500
shot sftp             --connect atlas --do "SFTP Browser" --shot-delay 6000
shot broadcast        --connect build-runner --connect atlas --do "Toggle Broadcast Mode" --shot-delay 5500
shot raw              --connect "Sensor feed" --shot-delay 5500
shot irc              --connect "Libera Chat" --shot-delay 5500
shot github           --connect GitHub --answer ghp_demo --shot-delay 6000
shot vnc              --connect "Studio desktop" --answer demo --shot-delay 6000
shot local            --run-local 'printf "\e[1;35mhello from the local shell\e[0m\n"; ls -la --color=always' --shot-delay 5000
shot palette          --open palette
shot quick            --open quick
shot editor-ssh       --open editor:ssh
shot editor-new       --open editor:new
shot editor-serial    --open editor:serial
shot editor-telnet    --open editor:telnet
shot editor-raw       --open editor:raw
shot editor-vnc       --open editor:vnc
shot editor-irc       --open editor:irc
shot editor-github    --open editor:github
for t in general terminal security knownhosts help about; do shot "settings-$t" --open "settings:$t"; done
shot keys             --open keys
shot snippets         --open snippets
shot triggers         --open triggers
shot recordings       --open recordings
shot export           --open export
