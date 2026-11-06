#!/bin/sh
# Claude Code SessionStart hook: keep the local polyforge daemon up and ask
# for this machine's interactive role.
#
# 1. `pf daemon ensure` (polyforge#174): a daemon that is down — after a
#    container or machine restart nothing else brings it back — is started
#    under its tmux restart loop (skipped when that loop already exists).
#    Bounded: ensure waits at most 15s, and `timeout` kills it at 20s.
# 2. `pf ops role interactive --if-unset`: the machine-role request (never
#    `--force` — taking the role over from another machine is the human's
#    explicit act). `--if-unset` leaves a role an operator chose explicitly
#    alone, across daemon restarts too (the daemon keeps it in its data dir):
#    `pf ops role runner` is not undone by the next start, compact or clear. Bounded by `timeout` too; a refusal or a daemon that is
#    still down is simply ignored.
#
# Both are best-effort and silent: the hook prints nothing, exits 0 and
# never blocks the session. Claude Code fires SessionStart on startup,
# resume, clear and compact.
#
# What it no longer does: it used to give the conversation a polyforge chat
# id (`pf chat init`), export PF_CHAT_ID through $CLAUDE_ENV_FILE and print
# that id as a JSON systemMessage/additionalContext object. The chat id was
# the routing label of the retired chat subscriptions; human decisions are
# now read from the daemon's decision feed in Pi Web's Polyforge panel, so a
# session needs no identity and the hook writes nothing to $CLAUDE_ENV_FILE.
#
# Step agents never get here: the daemon runs claude steps with --bare
# or --safe-mode (either skips hooks), and a process carrying the step
# marker PF_WI_ID exits at once as a second fence.

[ -n "${PF_WI_ID:-}" ] && exit 0
command -v pf >/dev/null 2>&1 || exit 0

# Drain the hook JSON on stdin (nothing in it is needed any more), so the
# writer never sees a closed pipe.
cat >/dev/null 2>&1 || true

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

if command -v timeout >/dev/null 2>&1; then
	timeout 10 pf ops role interactive --if-unset </dev/null >/dev/null 2>&1 || true
else
	pf ops role interactive --if-unset </dev/null >/dev/null 2>&1 || true
fi
exit 0
