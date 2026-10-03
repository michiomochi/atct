import ReactMarkdown, { type Components } from "react-markdown";
import remarkGfm from "remark-gfm";

// Raw HTML is never interpreted (no rehype-raw); unsafe URLs are dropped by react-markdown's default urlTransform.
const heading = "mt-4 font-display font-semibold text-ink-950 break-words";

const components: Components = {
  h1: ({ node: _, ...p }) => <h1 className={`${heading} text-lg`} {...p} />,
  h2: ({ node: _, ...p }) => <h2 className={`${heading} text-lg`} {...p} />,
  h3: ({ node: _, ...p }) => <h3 className={`${heading} text-base`} {...p} />,
  h4: ({ node: _, ...p }) => <h4 className={`${heading} text-base`} {...p} />,
  h5: ({ node: _, ...p }) => <h5 className={`${heading} text-base`} {...p} />,
  h6: ({ node: _, ...p }) => <h6 className={`${heading} text-base`} {...p} />,
  p: ({ node: _, ...p }) => <p className="mt-2 break-words" {...p} />,
  ul: ({ node: _, ...p }) => <ul className="mt-2 list-disc space-y-1 pl-6" {...p} />,
  ol: ({ node: _, ...p }) => <ol className="mt-2 list-decimal space-y-1 pl-6" {...p} />,
  li: ({ node: _, ...p }) => <li className="break-words" {...p} />,
  blockquote: ({ node: _, ...p }) => <blockquote className="mt-2 border-l-2 border-line pl-4 text-ink-700" {...p} />,
  hr: ({ node: _, ...p }) => <hr className="my-4 border-line" {...p} />,
  a: ({ node: _, ...p }) => (
    <a
      className="focus-ring break-words text-accent-700 underline decoration-accent-100 underline-offset-4 hover:decoration-accent-700"
      target="_blank"
      rel="noopener noreferrer"
      {...p}
    />
  ),
  img: ({ node: _, src, alt }) => {
    const url = typeof src === "string" ? src : "";
    return (
      <a
        className="focus-ring break-words text-accent-700 underline decoration-accent-100 underline-offset-4 hover:decoration-accent-700"
        href={url}
        target="_blank"
        rel="noopener noreferrer"
      >
        {alt ? `${alt} (${url})` : url}
      </a>
    );
  },
  table: ({ node: _, ...p }) => (
    <div className="table-scroll mt-2">
      <table className="w-full border-collapse text-left text-base" {...p} />
    </div>
  ),
  th: ({ node: _, ...p }) => <th className="border border-line bg-paper px-3 py-1.5 font-semibold text-ink-950" {...p} />,
  td: ({ node: _, ...p }) => <td className="border border-line px-3 py-1.5 align-top" {...p} />,
  pre: ({ node: _, ...p }) => (
    <pre className="mt-2 overflow-x-auto rounded-control border border-line bg-paper p-3 font-mono text-sm leading-5" {...p} />
  ),
  // Fenced code carries a language-* class or newlines; inline code gets the chip style.
  code: ({ node: _, className, children, ...p }) =>
    className || String(children).includes("\n") ? (
      <code className={className} {...p}>{children}</code>
    ) : (
      <code className="rounded-control bg-paper px-1 py-0.5 font-mono text-sm" {...p}>{children}</code>
    ),
};

export function Markdown({ children }: { children: string }) {
  return (
    <div className="min-w-0 break-words text-base leading-6 text-ink-800 [&>:first-child]:mt-0">
      <ReactMarkdown remarkPlugins={[remarkGfm]} components={components}>{children}</ReactMarkdown>
    </div>
  );
}
