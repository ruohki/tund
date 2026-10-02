import type { ReactNode } from "react";

// A small syntax highlighter for the short, static code examples in the
// dashboard: comments, strings, numbers, keywords and literals. Not a parser;
// good enough for snippets we write ourselves.

export type CodeLang = "js" | "python" | "go" | "rust" | "java" | "csharp";

const KEYWORDS: Record<CodeLang, string[]> = {
  js: ["const", "let", "var", "function", "return", "if", "else", "for", "of", "in", "new", "async", "await", "import", "from", "export", "throw", "try", "catch", "class"],
  python: ["def", "return", "if", "elif", "else", "for", "in", "not", "and", "or", "import", "from", "as", "class", "with", "raise", "try", "except", "async", "await", "lambda"],
  go: ["func", "return", "if", "else", "for", "range", "var", "const", "type", "struct", "package", "import", "go", "defer", "map", "chan", "switch", "case"],
  rust: ["fn", "let", "mut", "return", "if", "else", "for", "in", "match", "use", "async", "await", "pub", "struct", "impl", "move", "mod", "as", "ref"],
  java: ["public", "private", "protected", "static", "final", "class", "return", "if", "else", "for", "new", "throw", "throws", "import", "package", "void", "var", "try", "catch"],
  csharp: ["var", "return", "if", "else", "for", "foreach", "in", "new", "using", "public", "private", "static", "class", "async", "await", "throw", "namespace", "string", "int", "bool"],
};

const LITERALS = new Set(["true", "false", "null", "nil", "None", "True", "False", "undefined", "this", "self", "Self"]);

const STRING = String.raw`[fFrRbB$@]?(?:"(?:\\.|[^"\\\n])*"|'(?:\\.|[^'\\\n])*'|` + "`(?:\\\\.|[^`\\\\])*`)";
const NUMBER = String.raw`\b\d+(?:\.\d+)?\b`;
const WORD = String.raw`[A-Za-z_]\w*`;
const ANNOTATION = String.raw`@[A-Za-z_][\w.]*`;

function tokenizer(lang: CodeLang): RegExp {
  const comment = lang === "python" ? "#[^\\n]*" : String.raw`\/\/[^\n]*|\/\*[\s\S]*?\*\/`;
  return new RegExp(`(${comment})|(${STRING})|(${ANNOTATION})|(${NUMBER})|(${WORD})`, "g");
}

const tone = {
  comment: "text-muted italic",
  string: "text-[color:var(--syn-str)]",
  number: "text-[color:var(--syn-num)]",
  literal: "text-[color:var(--syn-lit)]",
  keyword: "text-[color:var(--syn-kw)]",
};

/** Code with highlighted tokens, for a <pre>. */
export function highlightCode(code: string, lang: CodeLang): ReactNode[] {
  const keywords = new Set(KEYWORDS[lang]);
  const re = tokenizer(lang);
  const out: ReactNode[] = [];
  let last = 0;
  let key = 0;
  let m: RegExpExecArray | null;
  const push = (text: string, cls?: string) => {
    out.push(cls ? (
      <span key={key++} className={cls}>
        {text}
      </span>
    ) : (
      text
    ));
  };
  while ((m = re.exec(code))) {
    if (m.index > last) push(code.slice(last, m.index));
    const [text, comment, string, annotation, number, word] = m;
    if (comment) push(text, tone.comment);
    else if (string) push(text, tone.string);
    else if (annotation) push(text, tone.literal);
    else if (number) push(text, tone.number);
    else if (word && keywords.has(word)) push(text, tone.keyword);
    else if (word && LITERALS.has(word)) push(text, tone.literal);
    else push(text);
    last = re.lastIndex;
  }
  if (last < code.length) push(code.slice(last));
  return out;
}
