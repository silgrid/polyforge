/**
 * Shared in-tab search primitives (pi#80).
 *
 * The chat pane and the file viewer both need the same machinery: build a
 * matcher from a query (+ case/regex toggles), collect DOM Ranges over a
 * content subtree in document order, and paint them with the CSS Custom
 * Highlight API — which, unlike DOM-node wrapping (mark.js style), never
 * fights React's ownership of the rendered nodes.
 */

export interface SearchToggles {
  caseSensitive: boolean;
  regex: boolean;
}

export interface SearchMatch {
  offset: number;
  length: number;
}

export interface SearchMatcher {
  /** All matches inside `text` (ascending). */
  findAll(text: string): SearchMatch[];
}

/**
 * Build a matcher from the raw query. An INVALID regex falls back to a
 * literal search rather than silently matching nothing: a typo in the
 * pattern should still find the typo text, not break the search.
 */
export function buildSearchMatcher(query: string, toggles: SearchToggles): SearchMatcher | null {
  const needle = query.trim();
  if (!needle) return null;
  if (toggles.regex) {
    try {
      // The "i" flag carries case-insensitivity; the pattern is kept verbatim
      // so character classes and anchors survive.
      const regexp = new RegExp(needle, toggles.caseSensitive ? "g" : "gi");
      return {
        findAll(text: string): SearchMatch[] {
          regexp.lastIndex = 0;
          const matches: SearchMatch[] = [];
          let match: RegExpExecArray | null;
          while ((match = regexp.exec(text)) !== null) {
            matches.push({ offset: match.index, length: match[0].length || 1 });
            if (match[0].length === 0) regexp.lastIndex++; // zero-width match guard
            if (matches.length > 5000) break; // pathological-input guard
          }
          return matches;
        },
      };
    } catch {
      // fall through to literal
    }
  }
  const escaped = needle.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const regexp = new RegExp(escaped, toggles.caseSensitive ? "g" : "gi");
  return {
    findAll(text: string): SearchMatch[] {
      regexp.lastIndex = 0;
      const matches: SearchMatch[] = [];
      let match: RegExpExecArray | null;
      while ((match = regexp.exec(text)) !== null) {
        matches.push({ offset: match.index, length: match[0].length });
        if (match[0].length === 0) regexp.lastIndex++;
        if (matches.length > 5000) break;
      }
      return matches;
    },
  };
}


const SKIP_TEXT_TAGS = new Set(["SCRIPT", "STYLE", "NOSCRIPT", "TEXTAREA", "INPUT"]);

/**
 * Collect one Range per match over every text node under `root`, in
 * document order. Text nodes are processed whole: a match never spans two
 * nodes (highlight quality is identical for normal prose; a word broken
 * across inline elements is a rare cosmetic miss, accepted for v1).
 */
export function collectTextMatches(root: Element | null | undefined, matcher: SearchMatcher): Range[] {
  if (!root || typeof document === "undefined") return [];
  const ranges: Range[] = [];
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT, {
    acceptNode(node) {
      const parent = node.parentElement;
      if (!parent || SKIP_TEXT_TAGS.has(parent.tagName)) return NodeFilter.FILTER_REJECT;
      if (!node.nodeValue || node.nodeValue.length === 0) return NodeFilter.FILTER_REJECT;
      return NodeFilter.FILTER_ACCEPT;
    },
  });
  let textNode = walker.nextNode();
  while (textNode) {
    const text = textNode.nodeValue ?? "";
    for (const match of matcher.findAll(text)) {
      const range = document.createRange();
      range.setStart(textNode, match.offset);
      range.setEnd(textNode, Math.min(match.offset + match.length, text.length));
      ranges.push(range);
    }
    textNode = walker.nextNode();
  }
  return ranges;
}

const HIGHLIGHT_ALL = "pi-tab-search-all";
const HIGHLIGHT_CURRENT = "pi-tab-search-current";

type HighlightRegistryLike = {
  set(name: string, highlight: unknown): HighlightRegistryLike;
  delete(name: string): boolean;
};
type HighlightCtor = new (...ranges: Range[]) => unknown;

function highlightRegistry(): HighlightRegistryLike | null {
  if (typeof CSS === "undefined" || !("highlights" in CSS)) return null;
  return CSS.highlights as unknown as HighlightRegistryLike;
}

let highlightCtor: HighlightCtor | null | undefined;
function getHighlightCtor(): HighlightCtor | null {
  if (highlightCtor !== undefined) return highlightCtor;
  highlightCtor = typeof Highlight === "undefined" ? null : (Highlight as unknown as HighlightCtor);
  return highlightCtor;
}

/**
 * Register the collected ranges as CSS Custom Highlights. Browsers without
 * the Highlight API (or during SSR) silently skip painting: navigation and
 * the counter keep working via the raw ranges.
 */
export function paintSearchHighlights(all: Range[], current: Range | null): void {
  const registry = highlightRegistry();
  const Ctor = getHighlightCtor();
  if (!registry || !Ctor) return;
  if (all.length === 0) {
    registry.delete(HIGHLIGHT_ALL);
  } else {
    registry.set(HIGHLIGHT_ALL, new Ctor(...all));
  }
  if (!current) {
    registry.delete(HIGHLIGHT_CURRENT);
  } else {
    registry.set(HIGHLIGHT_CURRENT, new Ctor(current));
  }
}

/** Element containing a range's start (nearest ancestor element). */
export function rangeHostElement(range: Range): HTMLElement | null {
  const node = range.startContainer;
  const element = node.nodeType === Node.ELEMENT_NODE ? (node as HTMLElement) : node.parentElement;
  return element ?? null;
}

/**
 * Scroll a range into view, vertically centered. Uses the range rect (not
 * element scrollIntoView) so partial element positions land correctly.
 */
export function scrollRangeToCenter(range: Range, container: HTMLElement | null): void {
  const host = rangeHostElement(range);
  if (!host) return;
  const rect = range.getBoundingClientRect();
  if (rect.height === 0 && rect.top === 0 && rect.bottom === 0) return; // detached range
  const scroller = container instanceof HTMLElement ? container : null;
  if (scroller) {
    const scrollerRect = scroller.getBoundingClientRect();
    const delta = rect.top + scroller.scrollTop - scrollerRect.top - (scrollerRect.height - rect.height) / 2;
    scroller.scrollTo({ top: Math.max(0, delta), behavior: "smooth" });
  } else {
    host.scrollIntoView({ block: "center", behavior: "smooth" });
  }
}
