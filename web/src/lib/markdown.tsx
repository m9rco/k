import * as React from "react";

// Markdown renders a SAFE, lightweight subset of markdown for assistant replies.
// It supports: headings, unordered/ordered lists, bold, italic, inline code,
// links (http/https only), and fenced code blocks (shown as a plain-text pre
// block). It intentionally does NOT support raw HTML — content is only ever
// placed as React text children, so injection is structurally impossible.
// Unclosed/half-streamed markers degrade to plain text rather than breaking.

type Block =
  | { kind: "heading"; level: number; text: string }
  | { kind: "ul"; items: string[] }
  | { kind: "ol"; items: string[] }
  | { kind: "code"; text: string }
  | { kind: "p"; text: string };

function parseBlocks(src: string): Block[] {
  const lines = src.replace(/\r\n/g, "\n").split("\n");
  const blocks: Block[] = [];
  let i = 0;
  while (i < lines.length) {
    const line = lines[i];

    // Fenced code block: ``` ... ``` (tolerates an unclosed fence by reading
    // to EOF, so a half-streamed block still shows as a code block).
    const fence = line.match(/^\s*```(.*)$/);
    if (fence) {
      const buf: string[] = [];
      i++;
      while (i < lines.length && !/^\s*```/.test(lines[i])) {
        buf.push(lines[i]);
        i++;
      }
      if (i < lines.length) i++; // consume closing fence if present
      blocks.push({ kind: "code", text: buf.join("\n") });
      continue;
    }

    // Heading: # .. ######
    const h = line.match(/^\s{0,3}(#{1,6})\s+(.*)$/);
    if (h) {
      blocks.push({ kind: "heading", level: h[1].length, text: h[2].trim() });
      i++;
      continue;
    }

    // Unordered list: consecutive "- " / "* " lines.
    if (/^\s{0,3}[-*]\s+/.test(line)) {
      const items: string[] = [];
      while (i < lines.length && /^\s{0,3}[-*]\s+/.test(lines[i])) {
        items.push(lines[i].replace(/^\s{0,3}[-*]\s+/, ""));
        i++;
      }
      blocks.push({ kind: "ul", items });
      continue;
    }

    // Ordered list: consecutive "1. " lines.
    if (/^\s{0,3}\d+\.\s+/.test(line)) {
      const items: string[] = [];
      while (i < lines.length && /^\s{0,3}\d+\.\s+/.test(lines[i])) {
        items.push(lines[i].replace(/^\s{0,3}\d+\.\s+/, ""));
        i++;
      }
      blocks.push({ kind: "ol", items });
      continue;
    }

    // Blank line: paragraph separator.
    if (line.trim() === "") {
      i++;
      continue;
    }

    // Paragraph: gather until a blank line or a block-starting line.
    const buf: string[] = [];
    while (
      i < lines.length &&
      lines[i].trim() !== "" &&
      !/^\s*```/.test(lines[i]) &&
      !/^\s{0,3}#{1,6}\s+/.test(lines[i]) &&
      !/^\s{0,3}[-*]\s+/.test(lines[i]) &&
      !/^\s{0,3}\d+\.\s+/.test(lines[i])
    ) {
      buf.push(lines[i]);
      i++;
    }
    blocks.push({ kind: "p", text: buf.join("\n") });
  }
  return blocks;
}

// inline parses bold/italic/inline-code/links into React nodes. Unmatched
// markers are emitted as literal text (streaming-safe).
function inline(src: string, keyPrefix: string): React.ReactNode[] {
  const nodes: React.ReactNode[] = [];
  let rest = src;
  let k = 0;
  // Ordered by specificity: code, bold, italic, link.
  const patterns: { re: RegExp; render: (m: RegExpMatchArray, key: string) => React.ReactNode }[] = [
    { re: /`([^`]+)`/, render: (m, key) => <code key={key} className="rounded bg-bg px-1 py-0.5 text-[12px]">{m[1]}</code> },
    { re: /\*\*([^*]+)\*\*/, render: (m, key) => <strong key={key}>{m[1]}</strong> },
    { re: /\*([^*\n]+)\*/, render: (m, key) => <em key={key}>{m[1]}</em> },
    {
      re: /\[([^\]]+)\]\((https?:\/\/[^\s)]+)\)/,
      render: (m, key) => (
        <a key={key} href={m[2]} target="_blank" rel="noopener noreferrer" className="text-accent underline">
          {m[1]}
        </a>
      ),
    },
  ];

  // Greedily find the earliest match among patterns; everything before is text.
  while (rest.length > 0) {
    let best: { idx: number; len: number; node: React.ReactNode } | null = null;
    for (const p of patterns) {
      const m = rest.match(p.re);
      if (m && m.index !== undefined) {
        if (best === null || m.index < best.idx) {
          best = { idx: m.index, len: m[0].length, node: p.render(m, `${keyPrefix}-${k}`) };
        }
      }
    }
    if (!best) {
      nodes.push(rest);
      break;
    }
    if (best.idx > 0) nodes.push(rest.slice(0, best.idx));
    nodes.push(best.node);
    k++;
    rest = rest.slice(best.idx + best.len);
  }
  return nodes;
}

export function Markdown({ text }: { text: string }) {
  const blocks = React.useMemo(() => parseBlocks(text), [text]);
  return (
    <>
      {blocks.map((b, i) => {
        const key = `b${i}`;
        switch (b.kind) {
          case "heading": {
            const cls = b.level <= 2 ? "text-sm font-semibold" : "text-[13px] font-semibold";
            return <p key={key} className={`${cls} mt-1`}>{inline(b.text, key)}</p>;
          }
          case "ul":
            return (
              <ul key={key} className="my-1 list-disc pl-5">
                {b.items.map((it, j) => <li key={j}>{inline(it, `${key}-${j}`)}</li>)}
              </ul>
            );
          case "ol":
            return (
              <ol key={key} className="my-1 list-decimal pl-5">
                {b.items.map((it, j) => <li key={j}>{inline(it, `${key}-${j}`)}</li>)}
              </ol>
            );
          case "code":
            return (
              <pre key={key} className="my-1 overflow-x-auto rounded bg-bg px-2 py-1.5 text-[12px] leading-relaxed">
                <code>{b.text}</code>
              </pre>
            );
          default:
            return <p key={key} className="whitespace-pre-wrap">{inline(b.text, key)}</p>;
        }
      })}
    </>
  );
}
