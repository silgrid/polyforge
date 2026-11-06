"""A small JSON Schema checker for the keywords pf-step-result-1.json uses.

The standard library has no JSON Schema validator; the Go tests validate the
Go commands' real result files with a full one. This checker covers exactly
the keywords the schema file uses (type, const, enum, required, properties,
additionalProperties, items, pattern, minLength, allOf, if/then, not) and
refuses a schema that uses any other, so it can never pass a document by
ignoring a keyword.
"""

import re
from typing import Any, Dict, List

_KNOWN = {
    "$schema", "title", "description", "type", "const", "enum", "required",
    "properties", "additionalProperties", "items", "pattern", "minLength",
    "allOf", "if", "then", "not",
}

_TYPES = {
    "object": lambda v: isinstance(v, dict),
    "array": lambda v: isinstance(v, list),
    "string": lambda v: isinstance(v, str),
    "integer": lambda v: isinstance(v, int) and not isinstance(v, bool),
}


def errors(schema: Dict[str, Any], doc: Any, where: str = "$") -> List[str]:
    """Every violation of schema by doc (empty = valid)."""
    unknown = set(schema) - _KNOWN
    if unknown:
        raise ValueError("schemacheck: unsupported keyword(s) %s at %s" % (sorted(unknown), where))
    out: List[str] = []
    if "type" in schema and not _TYPES[schema["type"]](doc):
        return [where + ": not " + schema["type"]]
    if "const" in schema and doc != schema["const"]:
        out.append("%s: %r is not %r" % (where, doc, schema["const"]))
    if "enum" in schema and doc not in schema["enum"]:
        out.append("%s: %r not in %r" % (where, doc, schema["enum"]))
    if isinstance(doc, str):
        if "pattern" in schema and not re.search(schema["pattern"], doc, re.ASCII):
            out.append("%s: %r does not match %s" % (where, doc, schema["pattern"]))
        if "minLength" in schema and len(doc) < schema["minLength"]:
            out.append("%s: shorter than %d" % (where, schema["minLength"]))
    if isinstance(doc, dict):
        for k in schema.get("required", []):
            if k not in doc:
                out.append("%s: missing %s" % (where, k))
        props = schema.get("properties", {})
        for k, v in doc.items():
            if k in props:
                out += errors(props[k], v, where + "." + k)
            elif schema.get("additionalProperties") is False:
                out.append("%s: unexpected %s" % (where, k))
            elif isinstance(schema.get("additionalProperties"), dict):
                out += errors(schema["additionalProperties"], v, where + "." + k)
    if isinstance(doc, list) and "items" in schema:
        for i, v in enumerate(doc):
            out += errors(schema["items"], v, "%s[%d]" % (where, i))
    for sub in schema.get("allOf", []):
        out += errors(sub, doc, where)
    if "if" in schema and not errors(schema["if"], doc, where):
        out += errors(schema.get("then", {}), doc, where)
    if "not" in schema and not errors(schema["not"], doc, where):
        out.append("%s: matches a schema it must not" % where)
    return out
