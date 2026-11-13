"""Typed JSON reads with the semantics the Go step commands had.

A Go command read a JSON file into a struct (``json.Unmarshal``); a script
that took one over reads the same file into a value of the same shape with
``decode(value, spec)``, so a file one accepted the other accepts, and one
it refused the other refuses:

- a key matches its field exactly, else ignoring ASCII case; unknown keys
  are ignored;
- ``null`` leaves a field its zero value (``""``, ``0``, ``False``, an
  absent list, map or pointer: ``None``);
- a value of the wrong JSON type is an error (``decode`` answers the first
  one), the field keeps its zero value and the rest is still read — Go's
  partial decode;
- an integer field takes a JSON integer only (``1.0`` and ``1e3`` are
  refused, as Go refuses them).

A spec is ``STR``, ``INT``, ``FLOAT``, ``BOOL``, ``ANY`` (the value as
parsed), ``List(spec)``, ``Map(spec)`` (string keys), ``Ptr(spec)`` (``None``
for null) or ``Struct({json_name: spec, ...})``, decoded to a dict holding
every field in the spec's order.
"""

from typing import Any, Dict, List as TList, Optional, Tuple


class _Scalar:
    def __init__(self, name: str) -> None:
        self.name = name

    def __repr__(self) -> str:
        return self.name


STR = _Scalar("string")
INT = _Scalar("int")
FLOAT = _Scalar("float64")
BOOL = _Scalar("bool")
ANY = _Scalar("interface {}")


class List:
    def __init__(self, elem: Any) -> None:
        self.elem = elem


class Map:
    def __init__(self, elem: Any) -> None:
        self.elem = elem


class Ptr:
    def __init__(self, elem: Any) -> None:
        self.elem = elem


class Struct:
    def __init__(self, fields: Dict[str, Any]) -> None:
        self.fields = fields
        self.fold = {k.lower(): k for k in fields}


def _kind(v: Any) -> str:
    if isinstance(v, bool):
        return "bool"
    if isinstance(v, (int, float)):
        return "number"
    if isinstance(v, str):
        return "string"
    if isinstance(v, list):
        return "array"
    if isinstance(v, dict):
        return "object"
    return "null"


def _type_name(spec: Any) -> str:
    if isinstance(spec, _Scalar):
        return spec.name
    if isinstance(spec, List):
        return "[]" + _type_name(spec.elem)
    if isinstance(spec, Map):
        return "map[string]" + _type_name(spec.elem)
    if isinstance(spec, Ptr):
        return "*" + _type_name(spec.elem)
    return "struct"


def zero(spec: Any) -> Any:
    """The zero value of spec."""
    if spec is STR:
        return ""
    if spec is INT:
        return 0
    if spec is FLOAT:
        return 0.0
    if spec is BOOL:
        return False
    if isinstance(spec, Struct):
        return {k: zero(s) for k, s in spec.fields.items()}
    return None


def _dec(v: Any, spec: Any, errs: TList[str]) -> Any:
    def bad() -> Any:
        errs.append("json: cannot unmarshal %s into Go value of type %s" % (_kind(v), _type_name(spec)))
        return zero(spec)

    if spec is ANY:
        return v
    if v is None:
        return zero(spec)
    if spec is STR:
        return v if isinstance(v, str) else bad()
    if spec is INT:
        return v if isinstance(v, int) and not isinstance(v, bool) else bad()
    if spec is FLOAT:
        return float(v) if isinstance(v, (int, float)) and not isinstance(v, bool) else bad()
    if spec is BOOL:
        return v if isinstance(v, bool) else bad()
    if isinstance(spec, Ptr):
        return _dec(v, spec.elem, errs)
    if isinstance(spec, List):
        if not isinstance(v, list):
            return bad()
        return [_dec(x, spec.elem, errs) for x in v]
    if isinstance(spec, Map):
        if not isinstance(v, dict):
            return bad()
        return {k: _dec(x, spec.elem, errs) for k, x in v.items()}
    if isinstance(spec, Struct):
        if not isinstance(v, dict):
            return bad()
        out = zero(spec)
        for k, x in v.items():
            name = k if k in spec.fields else spec.fold.get(k.lower())
            if name is not None:
                out[name] = _dec(x, spec.fields[name], errs)
        return out
    raise TypeError("not a decode spec: %r" % (spec,))


def decode(value: Any, spec: Any) -> Tuple[Any, Optional[str]]:
    """value (parsed JSON) read as spec: (the value, the first error or None)."""
    errs: TList[str] = []
    out = _dec(value, spec, errs)
    return out, (errs[0] if errs else None)
