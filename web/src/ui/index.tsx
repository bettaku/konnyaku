import { For, Show, type JSX } from "solid-js";
import { A } from "@solidjs/router";
import { percent, type Locale, type Stat, type Status } from "../api";

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
    <select name={props.name} value={props.value} onChange={(e) => props.onChange?.(e.currentTarget.value)}>
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
