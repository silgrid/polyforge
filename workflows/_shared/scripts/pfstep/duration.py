"""Go duration strings: parsed and formatted exactly as Go's time package does.

A duration is an int of nanoseconds (Go's time.Duration). ``parse`` is
time.ParseDuration ("1h30m", "1.5s", "-2m3.4s", "300ms"; units ns, us, µs,
μs, ms, s, m, h; a bare "0"), refusing what it refuses with its message;
``format`` is Duration.String ("1h30m0s", "1.5s", "300ms", "0s").
"""

NANOSECOND = 1
MICROSECOND = 1000 * NANOSECOND
MILLISECOND = 1000 * MICROSECOND
SECOND = 1000 * MILLISECOND
MINUTE = 60 * SECOND
HOUR = 60 * MINUTE

_MAX = (1 << 63) - 1  # the largest Go Duration
_MIN = -(1 << 63)

_UNITS = {
    "ns": NANOSECOND,
    "us": MICROSECOND,
    "µs": MICROSECOND,  # U+00B5 micro sign
    "μs": MICROSECOND,  # U+03BC Greek mu
    "ms": MILLISECOND,
    "s": SECOND,
    "m": MINUTE,
    "h": HOUR,
}


def _quote(s: str) -> str:
    """Go's time.quote: a non-ASCII or control character as its UTF-8
    bytes in \\x form, a quote or backslash escaped."""
    out = []
    for c in s:
        if ord(c) >= 0x80 or c < " ":
            out.append("".join("\\x%02x" % b for b in c.encode("utf-8", "surrogatepass")))
        elif c in '"\\':
            out.append("\\" + c)
        else:
            out.append(c)
    return '"' + "".join(out) + '"'


def _is_digit(c: str) -> bool:
    return "0" <= c <= "9"


def parse(s: str) -> int:
    """time.ParseDuration: the nanoseconds s names, or ValueError."""
    orig = s
    neg = False
    if s and s[0] in "-+":
        neg = s[0] == "-"
        s = s[1:]
    if s == "0":
        return 0
    if s == "":
        raise ValueError("time: invalid duration " + _quote(orig))
    total = 0
    while s:
        if not (s[0] == "." or _is_digit(s[0])):
            raise ValueError("time: invalid duration " + _quote(orig))
        # the integer part, [0-9]*
        i = 0
        v = 0
        while i < len(s) and _is_digit(s[i]):
            if v > (1 << 63) // 10:
                raise ValueError("time: invalid duration " + _quote(orig))
            v = v * 10 + ord(s[i]) - ord("0")
            if v > 1 << 63:
                raise ValueError("time: invalid duration " + _quote(orig))
            i += 1
        pre = i > 0
        s = s[i:]
        # the fraction, (\.[0-9]*)?: precision stops accumulating on overflow
        post = False
        f = 0
        scale = 1.0
        if s and s[0] == ".":
            s = s[1:]
            i = 0
            overflow = False
            while i < len(s) and _is_digit(s[i]):
                if not overflow:
                    if f > ((1 << 63) - 1) // 10:
                        overflow = True
                    else:
                        y = f * 10 + ord(s[i]) - ord("0")
                        if y > 1 << 63:
                            overflow = True
                        else:
                            f = y
                            scale *= 10
                i += 1
            post = i > 0
            s = s[i:]
        if not pre and not post:
            raise ValueError("time: invalid duration " + _quote(orig))
        # the unit
        i = 0
        while i < len(s) and not (s[i] == "." or _is_digit(s[i])):
            i += 1
        if i == 0:
            raise ValueError("time: missing unit in duration " + _quote(orig))
        u = s[:i]
        s = s[i:]
        if u not in _UNITS:
            raise ValueError("time: unknown unit " + _quote(u) + " in duration " + _quote(orig))
        unit = _UNITS[u]
        if v > (1 << 63) // unit:
            raise ValueError("time: invalid duration " + _quote(orig))
        v *= unit
        if f > 0:
            # float64, as Go: nanosecond-accurate for fractions of an hour
            v += int(float(f) * (float(unit) / scale))
            if v > 1 << 63:
                raise ValueError("time: invalid duration " + _quote(orig))
        total += v
        if total > 1 << 63:
            raise ValueError("time: invalid duration " + _quote(orig))
    if neg:
        return -total
    if total > _MAX:
        raise ValueError("time: invalid duration " + _quote(orig))
    return total


def _frac(v: int, prec: int) -> "tuple[str, int]":
    """The fraction of v/10**prec (".12" with trailing zeros dropped, "" when
    zero) and the integer part v//10**prec."""
    digits = ""
    printing = False
    for _ in range(prec):
        d = v % 10
        printing = printing or d != 0
        if printing:
            digits = str(d) + digits
        v //= 10
    return ("." + digits if printing else ""), v


def format(d: int) -> str:
    """Duration.String: "72h3m0.5s", "1.5s", "300ms", "1µs", "0s"."""
    if not _MIN <= d <= _MAX:
        raise ValueError("duration %d is outside Go's range" % d)
    neg = d < 0
    u = -d if neg else d
    if u < SECOND:
        if u == 0:
            return "0s"
        if u < MICROSECOND:
            prec, unit = 0, "ns"
        elif u < MILLISECOND:
            prec, unit = 3, "µs"
        else:
            prec, unit = 6, "ms"
        frac, u = _frac(u, prec)
        out = str(u) + frac + unit
    else:
        frac, u = _frac(u, 9)  # u: whole seconds
        out = str(u % 60) + frac + "s"
        u //= 60  # minutes
        if u > 0:
            out = str(u % 60) + "m" + out
            u //= 60  # hours: days have no fixed length
            if u > 0:
                out = str(u) + "h" + out
    return ("-" if neg else "") + out


def seconds(d: int) -> float:
    """d as float seconds (a timeout argument)."""
    return d / SECOND
