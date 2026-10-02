// Line diff (Myers' O(ND) algorithm) for comparing two captured requests.

export type DiffLine = { kind: "same" | "del" | "add"; text: string };
/** A run of unchanged lines folded away in the output. */
export type DiffFold = { kind: "fold"; count: number };

/** Above this many differences the diff gives up and shows a plain replacement. */
const MAX_EDITS = 2000;

/** Myers' shortest edit script between a and b, or null when it is too long. */
function myers(a: string[], b: string[]): DiffLine[] | null {
  const n = a.length;
  const m = b.length;
  const max = n + m;
  const off = max + 1;
  const v = new Int32Array(2 * max + 3);
  // trace[d] holds v for k in [-d, d] before step d.
  const trace: Int32Array[] = [];
  for (let d = 0; d <= Math.min(max, MAX_EDITS); d++) {
    trace.push(v.slice(off - d, off + d + 1));
    for (let k = -d; k <= d; k += 2) {
      let x = k === -d || (k !== d && v[off + k - 1] < v[off + k + 1]) ? v[off + k + 1] : v[off + k - 1] + 1;
      let y = x - k;
      while (x < n && y < m && a[x] === b[y]) {
        x++;
        y++;
      }
      v[off + k] = x;
      if (x >= n && y >= m) return backtrack(trace, a, b);
    }
  }
  return null;
}

function backtrack(trace: Int32Array[], a: string[], b: string[]): DiffLine[] {
  const out: DiffLine[] = [];
  let x = a.length;
  let y = b.length;
  for (let d = trace.length - 1; d >= 0; d--) {
    const v = trace[d];
    const at = (k: number) => v[k + d];
    const k = x - y;
    const prevK = k === -d || (k !== d && at(k - 1) < at(k + 1)) ? k + 1 : k - 1;
    const prevX = d === 0 ? 0 : at(prevK);
    const prevY = prevX - prevK;
    while (x > prevX && y > prevY) {
      out.push({ kind: "same", text: a[x - 1] });
      x--;
      y--;
    }
    if (d > 0) {
      if (x === prevX) out.push({ kind: "add", text: b[y - 1] });
      else out.push({ kind: "del", text: a[x - 1] });
    }
    x = prevX;
    y = prevY;
  }
  return out.reverse();
}

/** The line diff of two texts; unchanged lines stay in place. */
export function diffLines(left: string, right: string): DiffLine[] {
  const a = left.split("\n");
  const b = right.split("\n");
  // Common prefix and suffix are cheap to take off first.
  let start = 0;
  while (start < a.length && start < b.length && a[start] === b[start]) start++;
  let endA = a.length;
  let endB = b.length;
  while (endA > start && endB > start && a[endA - 1] === b[endB - 1]) {
    endA--;
    endB--;
  }
  const midA = a.slice(start, endA);
  const midB = b.slice(start, endB);
  const middle = myers(midA, midB) ?? [
    ...midA.map((text) => ({ kind: "del" as const, text })),
    ...midB.map((text) => ({ kind: "add" as const, text })),
  ];
  return [
    ...a.slice(0, start).map((text) => ({ kind: "same" as const, text })),
    ...middle,
    ...a.slice(endA).map((text) => ({ kind: "same" as const, text })),
  ];
}

/** Folds unchanged runs longer than 2×context, keeping context lines around changes. */
export function foldUnchanged(lines: DiffLine[], context = 3): (DiffLine | DiffFold)[] {
  const out: (DiffLine | DiffFold)[] = [];
  let i = 0;
  while (i < lines.length) {
    if (lines[i].kind !== "same") {
      out.push(lines[i++]);
      continue;
    }
    let j = i;
    while (j < lines.length && lines[j].kind === "same") j++;
    const head = i === 0 ? 0 : context;
    const tail = j === lines.length ? 0 : context;
    if (j - i > head + tail + 1) {
      out.push(...lines.slice(i, i + head), { kind: "fold", count: j - i - head - tail }, ...lines.slice(j - tail, j));
    } else {
      out.push(...lines.slice(i, j));
    }
    i = j;
  }
  return out;
}
