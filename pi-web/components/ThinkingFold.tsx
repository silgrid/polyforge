import type { CSSProperties, ReactNode } from "react";
import type { Dispatch, SetStateAction } from "react";

/**
 * The shared expansion state of a thinking block: collapsed (or expanded)
 * by the 「默认展开思考块」 preference, updated in place by its broadcast.
 * Signature stub (the behaviour lands with polyforge#53's implementation).
 */
export function useThinkingExpansion(): [boolean, Dispatch<SetStateAction<boolean>>] {
  return [false, () => {}];
}

/** The shared collapsible thinking treatment (signature stub, polyforge#53). */
export function ThinkingFold(_props: {
  text: string;
  label: string;
  expanded: boolean;
  onToggle: (next: boolean) => void;
  contentStyle?: CSSProperties;
  children?: ReactNode;
  trailing?: ReactNode;
}): ReactNode {
  return null;
}
