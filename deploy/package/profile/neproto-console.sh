# NeProto Constellation console autostart.
# Only an interactive root SSH session receives the console. File transfers,
# forced commands, cron, cloud-init, and local non-interactive shells are left
# untouched. Set NEPROTO_NO_AUTO_UI=1 or create
# /etc/neproto/console.no-autostart to bypass it.
if [ "${NEPROTO_NO_AUTO_UI:-0}" != 1 ] && \
   [ "${NEPROTO_CONSOLE_ACTIVE:-0}" != 1 ] && \
   [ -n "${SSH_TTY:-}" ] && \
   [ -z "${SSH_ORIGINAL_COMMAND:-}" ] && \
   [ -t 0 ] && [ -t 1 ] && \
   [ "$(id -u)" -eq 0 ] && \
   [ ! -e /etc/neproto/console.no-autostart ] && \
   [ -x /usr/local/bin/np ]; then
  NEPROTO_CONSOLE_ACTIVE=1
  export NEPROTO_CONSOLE_ACTIVE
  /usr/local/bin/np
  unset NEPROTO_CONSOLE_ACTIVE
fi
