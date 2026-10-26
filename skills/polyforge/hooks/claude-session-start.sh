#!/bin/sh
# Claude Code SessionStart hook: give this conversation a polyforge chat id.
#
# Reads the hook JSON on stdin (session_id, source), asks the local daemon
# for the chat id of this Claude session via `pf chat init` (a resumed
# session gets the same id), exports PF_CHAT_ID for every later Bash call
# through $CLAUDE_ENV_FILE, and prints one JSON object: systemMessage shows
# the id to the human, additionalContext hands it to the model (plain
# stdout would reach the model only).
# The id is a notification label, not a credential. When pf or the daemon
# is unavailable this exits 0 with no output.
#
# Before asking for the chat id it runs `pf daemon ensure` (polyforge#174):
# a daemon that is down — after a container or machine restart nothing
# else brings it back — is started under its tmux restart loop (skipped
# when that loop already exists). Bounded
# (ensure waits at most 15s, and `timeout` kills it at 20s), silent, and
# it never blocks the session: any failure just leaves the chat id unset.
# Claude Code fires SessionStart on startup, resume, clear and compact.
#
# Step agents never get here: the daemon runs claude steps with --bare
# or --safe-mode (either skips hooks), and a process carrying the step marker PF_WI_ID exits at
# once as a second fence.

[ -n "${PF_WI_ID:-}" ] && exit 0
command -v pf >/dev/null 2>&1 || exit 0

input=$(cat) || exit 0

# The restart loop already exists (tmux session polyforge-daemon, exact
# match, on the user's default tmux server): it owns the daemon — do not
# wait on ensure at every start/resume/compact while its daemon boots or
# keeps failing.
if command -v tmux >/dev/null 2>&1 && env -u TMUX -u TMUX_PANE tmux has-session -t =polyforge-daemon 2>/dev/null; then
	:
elif command -v timeout >/dev/null 2>&1; then
	timeout 20 pf daemon ensure --timeout 15s </dev/null >/dev/null 2>&1 || true
else
	pf daemon ensure --timeout 15s </dev/null >/dev/null 2>&1 || true
fi

if command -v jq >/dev/null 2>&1; then
	sid=$(printf '%s' "$input" | jq -r '.session_id // empty' 2>/dev/null)
else
	sid=$(printf '%s' "$input" | sed -n 's/.*"session_id"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1)
fi
[ -n "$sid" ] || exit 0

chat=$(pf chat init --harness claude --harness-session "$sid" 2>/dev/null) || exit 0
case "$chat" in
chat_*) ;;
*) exit 0 ;;
esac
# only a plain token may reach the env file
case "$chat" in
*[!A-Za-z0-9_]*) exit 0 ;;
esac

if [ -n "${CLAUDE_ENV_FILE:-}" ]; then
	printf 'export PF_CHAT_ID=%s\n' "$chat" >>"$CLAUDE_ENV_FILE" 2>/dev/null || true
fi
# stdout carries nothing but this object (Claude Code parses it as JSON);
# the id is a plain token, so no escaping is needed
printf '{"systemMessage":"polyforge chat id: %s","hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":"polyforge chat id: %s (PF_CHAT_ID is set for pf commands)"}}\n' "$chat" "$chat"
exit 0
