"""pfstep.gitmerge: the merge mechanics of the step scripts sync.py,
merge.py and revert.py (the Python successor of the Go package
internal/polyforge/gitmerge's merge half).

- ``git``: git and gh in a task repo's clone, as the lane's own identity,
  each call bounded by the run's context (``ctx``);
- ``conf``: a repository's ``.polyforge/integrate.conf``;
- ``merge``: the default branch's head merged in place (``sync``), and a
  merge in progress read, committed or aborted;
- ``resolve``: the conflicts with a mechanical answer (an append-only log,
  a pin holder hunk by hunk, a declared list);
- ``project`` and ``posixre``: a file as the projection publishes it, the
  list's rows interpreted, never run (POSIX leftmost-longest regexes);
- ``landing``: when a merged PR counts as the change landed;
- ``revert``: a source item's merges reverted on the default branch;
- ``gostr``: the Go string helpers the messages and parsers keep.

Its messages are the Go step commands' words, so a result file reads as
it did.
"""
