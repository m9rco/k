import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";

// cn merges conditional class names and resolves Tailwind conflicts.
export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

// stripMarkdown removes the common markdown markers an LLM may still emit despite
// the no-markdown system-prompt rule, so the plain-text chat bubble never shows
// literal **, ##, ``` etc. It is intentionally conservative: it unwraps emphasis
// and headings and drops code fences, leaving the inner text intact.
export function stripMarkdown(s: string): string {
  if (!s) return s;
  return s
    // fenced code blocks: drop the ``` lines, keep the code text
    .replace(/```[a-zA-Z0-9]*\n?/g, "")
    .replace(/```/g, "")
    // headings: "### Title" -> "Title"
    .replace(/^\s{0,3}#{1,6}\s+/gm, "")
    // bold/italic: **x** / __x__ / *x* / _x_ -> x
    .replace(/\*\*([^*]+)\*\*/g, "$1")
    .replace(/__([^_]+)__/g, "$1")
    .replace(/\*([^*\n]+)\*/g, "$1")
    .replace(/(^|[\s(])_([^_\n]+)_/g, "$1$2")
    // inline code: `x` -> x
    .replace(/`([^`]+)`/g, "$1")
    // list bullets at line start: "- x" / "* x" -> "x"
    .replace(/^\s{0,3}[-*]\s+/gm, "");
}
