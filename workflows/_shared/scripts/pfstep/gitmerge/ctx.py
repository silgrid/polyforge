"""A run's context and the merge mechanics' errors.

``Ctx`` is the deadline and the cancellation a run's commands are held to
(Go's context.Context): every git and gh call is bounded by what is left of
it, and one that finds it done fails at once with Go's words — "context
deadline exceeded" or "context canceled". An interrupt (SIGINT, or SIGTERM
/ SIGHUP once ``pfstep.runner.raise_on_termination`` is installed) that
arrives while a command runs kills the command and cancels the context, so
the run winds down as a Go command's does: each later call fails, and the
command reports itself interrupted.

``GoError`` is an error carried as the text the Go step command printed
(``fmt.Errorf`` chains included), so a result file's summaries and notes
read as they did.
"""

import time
from typing import Optional

DEADLINE_EXCEEDED = "context deadline exceeded"
CANCELED = "context canceled"


class GoError(Exception):
    """An error, carried as its message."""

    def wrap(self, prefix: str) -> "GoError":
        """The same kind of error, its message behind prefix (Go's
        ``fmt.Errorf("prefix: %w", err)``)."""
        out = type(self).__new__(type(self))
        GoError.__init__(out, prefix + ": " + str(self))
        out.__dict__.update(self.__dict__)
        return out


class CtxError(GoError):
    """The context is done: its deadline passed, or it was canceled."""


class Ctx:
    """A deadline (seconds, time.monotonic(); None = none) and a cancel."""

    def __init__(self, timeout: Optional[float] = None) -> None:
        self.deadline: Optional[float] = None if timeout is None else time.monotonic() + timeout
        self.canceled = False

    def cancel(self) -> None:
        self.canceled = True

    def err(self) -> Optional[str]:
        """Why the context is done (None = it is not)."""
        if self.canceled:
            return CANCELED
        if self.deadline is not None and time.monotonic() >= self.deadline:
            return DEADLINE_EXCEEDED
        return None

    def check(self) -> None:
        """Raise CtxError when the context is done."""
        e = self.err()
        if e is not None:
            raise CtxError(e)

    def remaining(self) -> Optional[float]:
        """Seconds left (None = no deadline)."""
        if self.deadline is None:
            return None
        return self.deadline - time.monotonic()


def background() -> Ctx:
    """A context that is never done (Go's context.Background())."""
    return Ctx()
