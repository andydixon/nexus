#!/bin/bash
# ForceCommand for the throwaway screenshot sshd: never a login shell (no ~/.profile, no byobu).
DEMO_HOME=$1 SFTP_SERVER=$2
export LC_ALL=C.UTF-8
case "$SSH_ORIGINAL_COMMAND" in
  *sftp*) exec "$SFTP_SERVER" -d "$DEMO_HOME" ;;
  ?*) cd "$DEMO_HOME" && HOME="$DEMO_HOME" exec bash --norc --noprofile -c "$SSH_ORIGINAL_COMMAND" ;;
esac
cd "$DEMO_HOME" || exit 1
export HOME="$DEMO_HOME" PS1='\[\e[1;32m\]andy@atlas\[\e[0m\]:\[\e[1;34m\]\w\[\e[0m\]\$ '
row() { # boxed line, padded on visible width
  local plain; plain=$(printf '%b' "$1" | sed 's/\x1b\[[0-9;]*m//g')
  printf '\e[1;36m│\e[0m  %b%*s\e[1;36m│\e[0m\n' "$1" $((44 - ${#plain})) ''
}
printf '\e[1;36m╭──────────────────────────────────────────────╮\e[0m\n'
row 'Welcome to \e[1matlas\e[0m · Debian 13 · kernel 6.12'
row 'Load \e[32m0.42\e[0m   Memory \e[33m61%\e[0m   Disk \e[32m38%\e[0m'
row 'Last deploy: \e[1m2 hours ago\e[0m by andy'
printf '\e[1;36m╰──────────────────────────────────────────────╯\e[0m\n\n'
printf '\e[1;32m$\e[0m ls -l --color\n'; ls -l --color=always
printf '\n\e[1;32m$\e[0m ./deploy.sh --dry-run\n'
for s in "Resolving build" "Uploading artefacts" "Rolling out to 4 nodes" "Health checks"; do
  printf '  \e[32m✔\e[0m %-26s \e[2m%s\e[0m\n' "$s" "$((RANDOM % 900 + 100))ms"
done
printf '  \e[33m⚠\e[0m 1 warning: \e[1mnginx.conf\e[0m has a deprecated directive\n'
printf '  \e[1;32mDone.\e[0m Nothing was actually deployed (dry run).\n\n'
exec bash --norc --noprofile -i
