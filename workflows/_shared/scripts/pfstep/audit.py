"""The verification steps' ``audit`` step parameter (PF_PARAM_AUDIT,
polyforge#138): the project owner's switch for the dependency-audit gate
the scope engine appends to every full report.

The lane declares the parameter (``audit: "on"``) on develop's
verification and integrate's full-gate; the composed final value — the
lane default, the project's ``steps.<project>.<step>.params.audit`` in its
workspace config, a ticket's ``pf override`` above both — reaches the
step's scripts as PF_PARAM_AUDIT. No key of a repository's
verify-scope.conf can turn the audit off or loosen it: the switch belongs
to the owner's side alone (a repository cannot water down its own audit);
the engine's own caller-side option (``--audit off``) is the only path,
and only these two steps ever pass it.

``on``, empty and unset are today's behavior byte for byte: no engine
option, no record, nothing new in any result. ``off`` drops the audit
command from the plan and leaves the closure note (the merged source
hint below) in the step's result — which layer turned it off, the
project's config or a pf override, is not distinguished (the owner's 1-B
ruling of 2026-11-19). Any other value is a named error before any check
or container starts (exit 2), never a silent on or off.
"""

from typing import Mapping

# The environment variable the composed parameter arrives as.
AUDIT_PARAM = "PF_PARAM_AUDIT"

# The fixed mark every named error of a bad value carries: the skills and
# the operator grep it, so a misconfigured project fails loudly and once.
AUDIT_INVALID_MARK = "pf-audit-param-invalid"

# The merged source hint a closed audit records (the owner's 1-B ruling,
# verbatim): the note states the audit is off and where the value came
# from, without distinguishing the project's config from a pf override.
AUDIT_OFF_NOTE = "PF_PARAM_AUDIT=off，来自项目配置或 pf override"


class AuditParamError(ValueError):
    """A PF_PARAM_AUDIT value that is neither on nor off (nor empty)."""


def read_audit(env: Mapping[str, str]) -> str:
    """The audit parameter of env: "on" (the default — an unset or empty
    value reads as on, today's behavior) or "off". Any other value raises
    AuditParamError (only the lower-case pair; nothing is trimmed or
    folded: "ON", "false" or " off" are errors, never a silent on or
    off)."""
    value = env.get(AUDIT_PARAM, "")
    if value in ("", "on"):
        return "on"
    if value == "off":
        return "off"
    raise AuditParamError("%s must be on or off, got %r" % (AUDIT_PARAM, value))
