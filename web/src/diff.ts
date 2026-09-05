export type DiffPart = { type: "eq" | "add" | "del"; text: string };

// CJK scripts have no word boundaries, so their characters are compared one by one.
const tokenize = (s: string) =>
  s.match(/\s+|[\p{Script=Han}\p{Script=Hiragana}\p{Script=Katakana}\p{Script=Hangul}]|[\p{L}\p{N}_]+|[^\s\p{L}\p{N}_]/gu) ?? [];

/** Word-level diff via LCS. Falls back to a whole-string replacement for very long inputs. */
export function diffWords(a: string, b: string): DiffPart[] {
  if (a === b) return a ? [{ type: "eq", text: a }] : [];
  const x = tokenize(a), y = tokenize(b);
  if (x.length * y.length > 250_000) return [{ type: "del", text: a }, { type: "add", text: b }];
  const n = x.length, m = y.length;
  const lcs: Uint16Array[] = Array.from({ length: n + 1 }, () => new Uint16Array(m + 1));
  for (let i = n - 1; i >= 0; i--) for (let j = m - 1; j >= 0; j--) lcs[i][j] = x[i] === y[j] ? lcs[i + 1][j + 1] + 1 : Math.max(lcs[i + 1][j], lcs[i][j + 1]);
  const out: DiffPart[] = [];
  const push = (type: DiffPart["type"], text: string) => {
    const last = out[out.length - 1];
    if (last && last.type === type) last.text += text;
    else out.push({ type, text });
  };
  let i = 0, j = 0;
  while (i < n && j < m) {
    if (x[i] === y[j]) { push("eq", x[i]); i++; j++; }
    else if (lcs[i + 1][j] >= lcs[i][j + 1]) push("del", x[i++]);
    else push("add", y[j++]);
  }
  while (i < n) push("del", x[i++]);
  while (j < m) push("add", y[j++]);
  return out;
}
