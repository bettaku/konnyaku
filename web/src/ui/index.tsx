import { For, Show, createMemo, createSignal, type JSX } from "solid-js";
import { A } from "@solidjs/router";
import { percent, type Locale, type Stat, type Status } from "../api";
import { setTheme, theme, type ThemeChoice } from "../theme";

export function Progress(props: { stat: Stat | undefined; compact?: boolean }) {
  const s = () => props.stat ?? { locale: "", total: 0, translated: 0, reviewed: 0, needs_review: 0 };
  const w = (n: number) => (s().total ? `${(n / s().total) * 100}%` : "0%");
  return (
    <div class="progress" title={`${s().translated}/${s().total} translated, ${s().reviewed} reviewed, ${s().needs_review} need review`}>
      <div class="bar">
        <span class="translated" style={{ width: w(s().translated) }} />
        <span class="reviewed" style={{ width: w(s().reviewed) }} />
        <span class="needs" style={{ width: w(s().needs_review), left: w(s().translated) }} />
      </div>
      <span class="pct">{percent(s())}%</span>
      <Show when={!props.compact}>
        <span class="muted small">{s().translated}/{s().total}</span>
      </Show>
    </div>
  );
}
export const Legend = () => (
  <div class="legend">
    <span><i class="sw-reviewed" />reviewed</span>
    <span><i class="sw-translated" />translated</span>
    <span><i class="sw-needs" />needs review</span>
    <span><i class="sw-untranslated" />untranslated</span>
  </div>
);
export const Badge = (props: { status: Status | string }) => <span class={"badge " + props.status}>{props.status.replace("_", " ")}</span>;

export function LocaleSelect(props: { locales: Locale[]; value?: string; name?: string; onChange?: (v: string) => void; exclude?: string[]; allowEmpty?: string }) {
  return (
    <select name={props.name} value={props.value ?? ""} onChange={(e) => props.onChange?.(e.currentTarget.value)}>
      <Show when={props.allowEmpty}><option value="">{props.allowEmpty}</option></Show>
      <For each={props.locales.filter((l) => !props.exclude?.includes(l.code))}>
        {(l) => <option value={l.code} selected={l.code === props.value}>{l.code} — {l.name}</option>}
      </For>
    </select>
  );
}
export function Crumbs(props: { items: Array<{ href?: string; label: string }> }) {
  return (
    <div class="crumbs">
      <For each={props.items}>{(it, i) => <>{i() > 0 && " / "}{it.href ? <A href={it.href}>{it.label}</A> : it.label}</>}</For>
    </div>
  );
}
export const Empty = (props: { children: JSX.Element }) => <div class="empty">{props.children}</div>;

/** Reads a submitted form into a plain object of trimmed strings. */
export const formData = (e: SubmitEvent) => {
  e.preventDefault();
  const form = e.currentTarget as HTMLFormElement;
  const out: Record<string, string> = {};
  new FormData(form).forEach((v, k) => (out[k] = typeof v === "string" ? v.trim() : ""));
  return { form, data: out };
};

/**
 * A button that opens a modal for picking several locales at once: search box,
 * checkbox list (already-added locales shown checked and disabled), and a footer
 * that submits the selection. `onAdd` resolves to true to close the dialog.
 */
export function LocalePicker(props: { locales: Locale[]; added: string[]; exclude?: string[]; onAdd: (codes: string[]) => Promise<boolean> }) {
  let dlg!: HTMLDialogElement;
  let input!: HTMLInputElement;
  const [query, setQuery] = createSignal("");
  const [picked, setPicked] = createSignal<string[]>([]);
  const [busy, setBusy] = createSignal(false);
  const isAdded = (code: string) => props.added.includes(code);
  // Rank: code prefix > code substring > name word prefix > name substring, so "pt" finds pt-BR before "Egypt".
  const rank = (l: Locale, q: string) => {
    const code = l.code.toLowerCase(), name = l.name.toLowerCase();
    if (code.startsWith(q)) return 0;
    if (code.includes(q)) return 1;
    if (name.split(/[\s()]+/).some((w) => w.startsWith(q))) return 2;
    return name.includes(q) ? 3 : -1;
  };
  const choices = createMemo(() => {
    const q = query().trim().toLowerCase();
    const pool = props.locales.filter((l) => !props.exclude?.includes(l.code));
    if (!q) return pool;
    return pool
      .map((l) => ({ l, r: rank(l, q) }))
      .filter((x) => x.r >= 0)
      .sort((a, b) => a.r - b.r || a.l.code.localeCompare(b.l.code))
      .map((x) => x.l);
  });
  const selectable = () => choices().filter((l) => !isAdded(l.code));
  const toggle = (code: string) => setPicked((p) => (p.includes(code) ? p.filter((c) => c !== code) : [...p, code]));
  const allVisiblePicked = () => selectable().length > 0 && selectable().every((l) => picked().includes(l.code));
  const toggleVisible = () => {
    const codes = selectable().map((l) => l.code);
    setPicked((p) => (allVisiblePicked() ? p.filter((c) => !codes.includes(c)) : [...new Set([...p, ...codes])]));
  };
  const open = () => { setQuery(""); setPicked([]); dlg.showModal(); input.focus(); };
  const submit = async () => {
    if (!picked().length || busy()) return;
    setBusy(true);
    const ok = await props.onAdd(picked());
    setBusy(false);
    if (ok) dlg.close();
  };
  const nameOf = (code: string) => props.locales.find((l) => l.code === code)?.name ?? code;

  return (
    <>
      <button type="button" class="secondary small" onClick={open}>+ Add locales</button>
      <dialog ref={dlg} class="picker" onClick={(e) => e.target === dlg && dlg.close()}>
        <div class="picker-head">
          <h3 class="m0">Add target locales</h3>
          <input
            ref={input}
            type="search"
            placeholder="Search by code or name (e.g. pt, Chinese)…"
            value={query()}
            onInput={(e) => setQuery(e.currentTarget.value)}
            onKeyDown={(e) => {
              // Enter toggles the only (or first) selectable match, so keyboard-only picking works.
              if (e.key === "Enter") { e.preventDefault(); const first = selectable()[0]; if (first) toggle(first.code); }
            }}
          />
          <div class="row center between small">
            <label class="check small">
              <input type="checkbox" checked={allVisiblePicked()} disabled={!selectable().length} onChange={toggleVisible} />
              Select all {query() ? "matches" : "locales"} ({selectable().length})
            </label>
            <span class="muted">{picked().length} selected</span>
          </div>
          <Show when={picked().length}>
            <div class="chips">
              <For each={picked()}>
                {(c) => <button type="button" class="chip active" title={`Unselect ${nameOf(c)}`} onClick={() => toggle(c)}>{c} ×</button>}
              </For>
            </div>
          </Show>
        </div>
        <ul class="list picker-list">
          <For each={choices()} fallback={<li><Empty>No locales match “{query()}”.</Empty></li>}>
            {(l) => (
              <li>
                <label class={"check" + (isAdded(l.code) ? " muted" : "")}>
                  <input type="checkbox" checked={isAdded(l.code) || picked().includes(l.code)} disabled={isAdded(l.code)} onChange={() => toggle(l.code)} />
                  <code>{l.code}</code> {l.name}
                  <Show when={isAdded(l.code)}><span class="badge ml">added</span></Show>
                </label>
              </li>
            )}
          </For>
        </ul>
        <div class="row center between picker-foot">
          <span class="muted small">Esc to close</span>
          <div class="row center">
            <button type="button" class="secondary" onClick={() => dlg.close()}>Cancel</button>
            <button type="button" disabled={!picked().length || busy()} onClick={submit}>
              {busy() ? "Adding…" : `Add ${picked().length || ""} locale${picked().length === 1 ? "" : "s"}`}
            </button>
          </div>
        </div>
      </dialog>
    </>
  );
}

/** Segmented control for the theme preference (system / light / dark). */
export function ThemeSwitch(props: { compact?: boolean }) {
  const options: Array<{ value: ThemeChoice; label: string; icon: string }> = [
    { value: "auto", label: "System", icon: "◐" },
    { value: "light", label: "Light", icon: "☀" },
    { value: "dark", label: "Dark", icon: "☾" },
  ];
  return (
    <div class={"segmented" + (props.compact ? " compact" : "")} role="radiogroup" aria-label="Theme">
      <For each={options}>
        {(o) => (
          <button
            type="button"
            role="radio"
            aria-checked={theme() === o.value}
            class={theme() === o.value ? "on" : ""}
            title={o.label}
            onClick={() => setTheme(o.value)}
          >
            <span aria-hidden="true">{o.icon}</span>{props.compact ? "" : ` ${o.label}`}
            <Show when={props.compact}><span class="sr-only">{o.label}</span></Show>
          </button>
        )}
      </For>
    </div>
  );
}
