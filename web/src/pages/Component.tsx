import { For, Show, createEffect, createMemo, createResource, createSignal, on } from "solid-js";
import { A, useNavigate, useParams, useSearchParams } from "@solidjs/router";
import { api, fmtTime, type GlossaryTerm, type HistoryEntry, type Status, type Unit } from "../api";
import { diffWords } from "../diff";
import { useAction, useSession } from "../session";
import { Badge, Crumbs, Empty, LocaleSelect, Progress, formData } from "../ui";

const STATUSES: Array<{ value: string; label: string }> = [
  { value: "", label: "All" },
  { value: "untranslated", label: "Untranslated" },
  { value: "needs_review", label: "Needs review" },
  { value: "translated", label: "Translated" },
  { value: "reviewed", label: "Reviewed" },
];
type Insert = { unitId: number; value: string; seq: number };

export function ComponentPage() {
  const params = useParams();
  const id = () => Number(params.id);
  const [search, setSearch] = useSearchParams<{ locale?: string; q?: string; status?: string; offset?: string }>();
  const { notify } = useSession();
  const run = useAction();
  const navigate = useNavigate();
  const [detail, { refetch: refetchDetail }] = createResource(id, api.component);
  const [stats, { refetch: refetchStats }] = createResource(id, api.componentStats);
  const targets = () => detail()?.locales ?? [];
  const locale = createMemo(() => {
    const wanted = typeof search.locale === "string" ? search.locale : "";
    if (wanted) return wanted;
    return targets()[0]?.code ?? detail()?.project.source_locale ?? "";
  });
  const isSource = () => locale() === detail()?.project.source_locale;
  const role = () => detail()?.role ?? "viewer";
  const canEdit = () => !isSource() && (role() === "translator" || role() === "manager" || role() === "admin");
  const canReview = () => role() === "manager" || role() === "admin";
  const manage = () => canReview();
  const query = () => (typeof search.q === "string" ? search.q : "");
  const status = () => (typeof search.status === "string" ? search.status : "");
  const offset = () => Number(search.offset ?? 0) || 0;

  const [page, { refetch: refetchUnits, mutate }] = createResource(
    () => (locale() ? { id: id(), locale: locale(), q: query(), status: status(), offset: offset() } : null),
    (p) => api.units(p.id, p),
  );
  const [glossaryAll] = createResource(() => (detail() && locale() && !isSource() ? { p: detail()!.project.id, l: locale() } : null), (p) => api.glossary(p.p, p.l));
  const [selected, setSelected] = createSignal<Unit | null>(null);
  const [history, { refetch: refetchHistory }] = createResource(() => (selected() ? { u: selected()!.id, l: locale() } : null), (p) => api.unitHistory(p.u, p.l));
  const [assist] = createResource(() => (selected() ? { u: selected()!.id, l: locale() } : null), (p) => api.assist(p.u, p.l));
  const [activity, { refetch: refetchActivity }] = createResource(() => (locale() ? { id: id(), l: locale() } : null), (p) => api.componentHistory(p.id, p.l));
  const [insert, setInsert] = createSignal<Insert | null>(null);
  createEffect(on(locale, () => setSelected(null)));

  let searchTimer: ReturnType<typeof setTimeout> | undefined;
  const onSearch = (v: string) => {
    clearTimeout(searchTimer);
    searchTimer = setTimeout(() => setSearch({ q: v || undefined, offset: undefined }), 250);
  };
  const setStatus = (v: string) => setSearch({ status: v || undefined, offset: undefined });
  const setLocale = (v: string) => setSearch({ locale: v, offset: undefined });
  const statFor = (code: string) => stats()?.find((s) => s.locale === code);

  const saved = (u: Unit, value: string, st: Status, version: number) => {
    const updated = { ...u, value, status: st, version, updated_at: new Date().toISOString() };
    mutate((p) => p && { ...p, units: p.units.map((x) => (x.id === u.id ? updated : x)) });
    refetchStats();
    refetchActivity();
    if (selected()?.id === u.id) {
      setSelected(updated);
      refetchHistory();
    }
  };
  const useValue = (value: string) => {
    const u = selected();
    if (u) setInsert({ unitId: u.id, value, seq: (insert()?.seq ?? 0) + 1 });
  };
  const restore = async (h: HistoryEntry) => {
    const u = selected();
    if (!u) return;
    const st: Status = h.status === "reviewed" && !canReview() ? "translated" : h.status === "untranslated" ? "translated" : h.status;
    await run(async () => {
      const r = await api.saveTranslation(u.id, locale(), { value: h.value, status: st, version: u.version });
      saved(u, r.value, r.status, r.version);
    }, `Restored v${h.version}`);
  };
  const [fillInfo, setFillInfo] = createSignal<{ untranslated: number; matches: number } | null>(null);
  const [filling, setFilling] = createSignal(false);
  createEffect(on(locale, () => setFillInfo(null)));
  const checkFill = async () => {
    setFilling(true);
    try {
      const r = await api.autofill(id(), locale(), true);
      setFillInfo(r);
      if (!r.untranslated) notify("Nothing left to translate in this locale", true);
      else if (!r.matches) notify(`No exact translation-memory matches for the ${r.untranslated} untranslated unit(s)`, true);
    } catch (err) {
      notify(err instanceof Error ? err.message : String(err));
    } finally {
      setFilling(false);
    }
  };
  const applyFill = async (st: "needs_review" | "translated") => {
    const info = fillInfo();
    if (!info || !confirm(`Fill ${info.matches} of ${info.untranslated} untranslated unit(s) from exact translation-memory matches as "${st.replace("_", " ")}"?`)) return;
    setFilling(true);
    try {
      const r = await api.autofill(id(), locale(), false, st);
      notify(`Filled ${r.filled} unit(s)`, true);
      setFillInfo(null);
      refetchUnits(); refetchStats(); refetchActivity();
    } catch (err) {
      notify(err instanceof Error ? err.message : String(err));
    } finally {
      setFilling(false);
    }
  };
  const importFile = async (e: SubmitEvent) => {
    const { form } = formData(e);
    const file = (form.elements.namedItem("file") as HTMLInputElement).files?.[0];
    const loc = (form.elements.namedItem("locale") as HTMLSelectElement).value;
    if (!file) return notify("choose a file");
    const ok = await run(async () => {
      const r = await api.importFile(id(), loc, file);
      notify(`Imported ${r.imported} entries into ${loc}`, true);
    });
    if (ok) { form.reset(); refetchDetail(); refetchStats(); refetchUnits(); refetchActivity(); }
  };
  const [editing, setEditing] = createSignal(false);
  const [repositories] = createResource(() => (editing() && detail() ? detail()!.project.id : null), api.repositories);
  const saveSettings = async (e: SubmitEvent) => {
    const { data } = formData(e);
    const ok = await run(() => api.updateComponent(id(), { name: data.name, file_pattern: data.file_pattern, repository_id: data.repository_id ? Number(data.repository_id) : 0 }), "Saved");
    if (ok) { setEditing(false); refetchDetail(); }
  };
  const remove = async () => {
    const d = detail();
    if (!d || !confirm(`Delete component "${d.component.name}" and its translations?`)) return;
    if (await run(() => api.deleteComponent(id()))) navigate(`/projects/${d.project.id}`);
  };

  return (
    <Show when={detail()} fallback={<Empty>Loading…</Empty>}>
      {(d) => (
        <>
          <Crumbs items={[{ href: "/projects", label: "Projects" }, { href: `/projects/${d().project.id}`, label: d().project.name }, { label: d().component.name }]} />
          <div class="row center">
            <h1 class="m0">{d().component.name}</h1>
            <span class="badge">{d().component.format}</span>
            <Show when={d().component.repository_id}>{(rid) => <A class="small" href={`/repositories/${rid()}`}>repository</A>}</Show>
            <A class="small" href={`/projects/${d().project.id}/glossary${isSource() ? "" : `?locale=${encodeURIComponent(locale())}`}`}>glossary</A>
            <span class="grow" />
            <a class="small" href={api.exportUrl(id(), locale())}>Export {locale()}</a>
            <Show when={manage()}><button class="ghost small" onClick={() => setEditing(!editing())}>Settings</button></Show>
          </div>

          <Show when={editing()}>
            <div class="panel">
              <form class="row" onSubmit={saveSettings}>
                <label>Name<input name="name" value={d().component.name} required /></label>
                <label>Repository
                  <select name="repository_id">
                    <option value="">none</option>
                    <For each={repositories()}>{(r) => <option value={r.id} selected={r.id === d().component.repository_id}>{r.name}</option>}</For>
                  </select>
                </label>
                <label class="grow">File pattern<input name="file_pattern" value={d().component.file_pattern} required /></label>
                <button type="submit">Save</button>
                <button type="button" class="danger" onClick={remove}>Delete component</button>
              </form>
              <form class="row mt" onSubmit={importFile}>
                <label>Import into<LocaleSelect name="locale" locales={[{ code: d().project.source_locale, name: "source" }, ...targets()]} value={locale()} /></label>
                <label>File<input name="file" type="file" required /></label>
                <button type="submit" class="secondary">Import</button>
                <span class="muted small">Import the source catalog first; target imports update known keys only.</span>
              </form>
            </div>
          </Show>

          <div class="tabs" role="tablist">
            <button class={"tab" + (isSource() ? " active" : "")} onClick={() => setLocale(d().project.source_locale)}>
              {d().project.source_locale} <span class="pct">source</span>
            </button>
            <For each={targets()}>
              {(l) => (
                <button class={"tab" + (locale() === l.code ? " active" : "")} onClick={() => setLocale(l.code)} title={l.name}>
                  {l.code}
                  <Show when={statFor(l.code)}>{(s) => <span class="pct">{s().total ? Math.round((s().translated / s().total) * 100) : 0}%</span>}</Show>
                </button>
              )}
            </For>
            <Show when={!targets().length}><span class="muted small pad">No target locales — add them on the project page.</span></Show>
          </div>
          <div class="row center mb">
            <Show when={!isSource() && statFor(locale())}>{(s) => <div class="narrow grow"><Progress stat={s()} /></div>}</Show>
            <Show when={canEdit()}>
              <Show when={fillInfo()?.matches} fallback={<button class="secondary small" disabled={filling()} onClick={checkFill} title="Copy translations of identical source strings from other components and projects">Fill from translation memory…</button>}>
                <span class="small">{fillInfo()!.matches} of {fillInfo()!.untranslated} untranslated units have exact matches:</span>
                <button class="small" disabled={filling()} onClick={() => applyFill("needs_review")}>Fill as needs review</button>
                <button class="secondary small" disabled={filling()} onClick={() => applyFill("translated")}>Fill as translated</button>
                <button class="ghost small" onClick={() => setFillInfo(null)}>Cancel</button>
              </Show>
            </Show>
          </div>

          <div class="row center mb">
            <input class="grow" type="search" placeholder="Search key, source or translation" value={query()} onInput={(e) => onSearch(e.currentTarget.value)} />
            <div class="chips">
              <For each={STATUSES}>{(s) => <button class={"chip" + (status() === s.value ? " active" : "")} onClick={() => setStatus(s.value)}>{s.label}</button>}</For>
            </div>
          </div>

          <div class="editor">
            <div>
              <Show when={page()} fallback={<Empty>Loading…</Empty>}>
                {(p) => (
                  <>
                    <div class="muted small mb">
                      {p().total} unit{p().total === 1 ? "" : "s"}{query() || status() ? " matching" : ""}
                      <Show when={!canEdit() && !isSource()}> · read-only ({role()})</Show>
                    </div>
                    <Show when={p().units.length} fallback={<Empty>{p().total === 0 && !query() && !status() ? "No units yet. Import the source catalog from Settings or sync the repository." : "No units match."}</Empty>}>
                      <For each={p().units}>
                        {(u) => (
                          <UnitCard unit={u} locale={locale()} source={isSource()} editable={canEdit()} reviewer={canReview()}
                            glossary={glossaryAll() ?? []} insert={insert()}
                            selected={selected()?.id === u.id} onSelect={() => setSelected(selected()?.id === u.id ? null : u)} onSaved={saved} />
                        )}
                      </For>
                    </Show>
                    <Show when={p().total > p().limit}>
                      <div class="pager">
                        <button class="secondary small" disabled={offset() === 0} onClick={() => setSearch({ offset: String(Math.max(0, offset() - p().limit)) || undefined })}>‹ Prev</button>
                        <span class="muted small">{offset() + 1}–{Math.min(offset() + p().limit, p().total)} of {p().total}</span>
                        <button class="secondary small" disabled={offset() + p().limit >= p().total} onClick={() => setSearch({ offset: String(offset() + p().limit) })}>Next ›</button>
                      </div>
                    </Show>
                  </>
                )}
              </Show>
            </div>
            <aside class="side">
              <Show when={selected()} fallback={
                <div class="panel">
                  <h3>Recent changes · {locale()}</h3>
                  <Show when={activity()?.length} fallback={<p class="muted small">No changes yet. Select a unit to see glossary hits, translation memory and history.</p>}>
                    <ul class="history list">
                      <For each={activity()?.slice(0, 20)}>
                        {(h) => (
                          <li>
                            <div class="meta">{fmtTime(h.changed_at)} · {h.changed_by_name || "system"} · <Badge status={h.status} /></div>
                            <code class="small">{h.key}</code>
                            <div class="value small">{h.value}</div>
                          </li>
                        )}
                      </For>
                    </ul>
                  </Show>
                </div>
              }>
                {(u) => (
                  <div class="panel assist">
                    <div class="row center"><code class="grow small">{u().key}</code><button class="ghost small" onClick={() => setSelected(null)}>×</button></div>
                    <h4>Glossary</h4>
                    <Show when={assist()} fallback={<p class="muted small">Loading…</p>}>
                      {(a) => (
                        <>
                          <Show when={a().glossary.length} fallback={<p class="muted small">No glossary terms in this string.</p>}>
                            <For each={a().glossary}>
                              {(g) => (
                                <div class="match">
                                  <div><strong>{g.term}</strong> → {g.translation}
                                    <Show when={u().value}>{" "}<span class={u().value.includes(g.translation) ? "term-ok" : "term-missing"} title={u().value.includes(g.translation) ? "translation contains the term" : "translation does not contain the glossary wording"}>{u().value.includes(g.translation) ? "✓" : "⚠"}</span></Show>
                                  </div>
                                  <Show when={g.note}><div class="meta">{g.note}</div></Show>
                                  <Show when={canEdit()}><div class="meta"><button class="ghost small" onClick={() => useValue(g.translation)}>Insert</button></div></Show>
                                </div>
                              )}
                            </For>
                          </Show>
                          <h4>Translation memory</h4>
                          <Show when={a().memory.length} fallback={<p class="muted small">No similar strings translated into {locale()} yet.</p>}>
                            <For each={a().memory}>
                              {(m) => (
                                <div class="match">
                                  <div class="src"><Show when={m.source !== u().source} fallback={<span class="muted">exact match</span>}><span class="diff"><For each={diffWords(u().source, m.source)}>{(p) => p.type === "eq" ? p.text : p.type === "add" ? <ins>{p.text}</ins> : <del>{p.text}</del>}</For></span></Show></div>
                                  <div class="val">{m.value}</div>
                                  <div class="meta">
                                    <span class="score">{Math.round(m.score * 100)}%</span>
                                    <Badge status={m.status} />
                                    <span>{m.project_name} / {m.component_name}</span>
                                    <Show when={canEdit()}><button class="ghost small" onClick={() => useValue(m.value)}>Use</button></Show>
                                  </div>
                                </div>
                              )}
                            </For>
                          </Show>
                        </>
                      )}
                    </Show>
                    <h4>History</h4>
                    <Show when={history()} fallback={<p class="muted small">Loading…</p>}>
                      {(hs) => (
                        <Show when={hs().length} fallback={<p class="muted small">No history for this locale yet.</p>}>
                          <ul class="history list">
                            <For each={hs()}>
                              {(h, i) => {
                                const previous = () => hs()[i() + 1]?.value ?? "";
                                const current = () => h.version === u().version && h.value === u().value;
                                return (
                                  <li>
                                    <div class="meta">
                                      v{h.version} · {fmtTime(h.changed_at)} · {h.changed_by_name || "system"} · <Badge status={h.status} />
                                      <Show when={current()}> · <span class="term-ok">current</span></Show>
                                      <Show when={!current() && canEdit()}> · <button class="ghost small" onClick={() => restore(h)} title="Save this version as the current translation">Restore</button></Show>
                                    </div>
                                    <div class="value diff">
                                      <Show when={i() < hs().length - 1} fallback={h.value || <span class="muted">(empty)</span>}>
                                        <For each={diffWords(previous(), h.value)}>{(p) => p.type === "eq" ? p.text : p.type === "add" ? <ins>{p.text}</ins> : <del>{p.text}</del>}</For>
                                      </Show>
                                    </div>
                                  </li>
                                );
                              }}
                            </For>
                          </ul>
                        </Show>
                      )}
                    </Show>
                  </div>
                )}
              </Show>
            </aside>
          </div>
        </>
      )}
    </Show>
  );
}

/** Splits a source string into text and highlighted glossary terms (longest terms win). */
function highlight(source: string, terms: GlossaryTerm[]): Array<{ text: string; term?: GlossaryTerm }> {
  if (!terms.length) return [{ text: source }];
  const lower = source.toLowerCase();
  const sorted = [...terms].sort((a, b) => b.term.length - a.term.length);
  const out: Array<{ text: string; term?: GlossaryTerm }> = [];
  let pos = 0;
  while (pos < source.length) {
    let best: { at: number; term: GlossaryTerm } | null = null;
    for (const t of sorted) {
      const at = lower.indexOf(t.term.toLowerCase(), pos);
      if (at >= 0 && (!best || at < best.at)) best = { at, term: t };
    }
    if (!best) break;
    if (best.at > pos) out.push({ text: source.slice(pos, best.at) });
    out.push({ text: source.slice(best.at, best.at + best.term.term.length), term: best.term });
    pos = best.at + best.term.term.length;
  }
  if (pos < source.length) out.push({ text: source.slice(pos) });
  return out;
}

function UnitCard(props: {
  unit: Unit; locale: string; source: boolean; editable: boolean; reviewer: boolean; selected: boolean;
  glossary: GlossaryTerm[]; insert: Insert | null;
  onSelect: () => void; onSaved: (u: Unit, value: string, status: Status, version: number) => void;
}) {
  const { notify } = useSession();
  const [value, setValue] = createSignal(props.unit.value);
  const [status, setStatus] = createSignal<Status>(props.unit.status === "untranslated" ? "translated" : props.unit.status);
  const [busy, setBusy] = createSignal(false);
  let textarea: HTMLTextAreaElement | undefined;
  createEffect(on(() => props.unit, (u) => { setValue(u.value); setStatus(u.status === "untranslated" ? "translated" : u.status); }));
  createEffect(on(() => props.insert, (ins) => {
    if (ins && ins.unitId === props.unit.id) {
      setValue(ins.value);
      textarea?.focus();
    }
  }, { defer: true }));
  const dirty = () => value() !== props.unit.value || (props.unit.status !== "untranslated" && status() !== props.unit.status);
  const parts = createMemo(() => highlight(props.unit.source, props.source ? [] : props.glossary));
  const save = async () => {
    if (busy()) return;
    setBusy(true);
    try {
      const r = await api.saveTranslation(props.unit.id, props.locale, { value: value(), status: status(), version: props.unit.version });
      props.onSaved(props.unit, r.value, r.status, r.version);
      notify("Saved", true);
    } catch (err) {
      notify(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };
  const suggest = async (provider: "openai" | "google") => {
    setBusy(true);
    try {
      const r = await api.suggest(props.unit.id, provider, props.locale);
      setValue(r.value);
      notify(`Suggestion from ${provider} inserted — review, then save`, true);
    } catch (err) {
      notify(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };
  return (
    <div class={"unit" + (props.selected ? " selected" : "") + (dirty() ? " dirty" : "")}>
      <div class="head">
        <span class="key">{props.unit.key}</span>
        <Badge status={props.unit.status} />
        <Show when={props.unit.updated_at}><span class="muted small">{fmtTime(props.unit.updated_at)}</span></Show>
        <span class="grow" />
        <Show when={!props.source}><button class="ghost small" onClick={props.onSelect}>{props.selected ? "Hide details" : "Details"}</button></Show>
      </div>
      <div class="source">
        <For each={parts()}>{(p) => p.term ? <mark class="term" title={`${p.term.term} → ${p.term.translation}${p.term.note ? ` (${p.term.note})` : ""}`}>{p.text}</mark> : p.text}</For>
      </div>
      <Show when={!props.source} fallback={<div class="muted small">source string</div>}>
        <div>
          <textarea ref={textarea} value={value()} disabled={!props.editable} onInput={(e) => setValue(e.currentTarget.value)}
            onFocus={() => { if (!props.selected) props.onSelect(); }}
            onKeyDown={(e) => { if ((e.metaKey || e.ctrlKey) && e.key === "Enter") { e.preventDefault(); save(); } }} />
          <Show when={props.editable}>
            <div class="actions">
              <select value={status()} onChange={(e) => setStatus(e.currentTarget.value as Status)}>
                <option value="translated">translated</option>
                <option value="needs_review">needs review</option>
                <option value="reviewed" disabled={!props.reviewer}>reviewed</option>
              </select>
              <button class="small" disabled={busy() || !dirty()} onClick={save} title="Ctrl+Enter">Save</button>
              <button class="secondary small" disabled={busy()} onClick={() => suggest("openai")}>Suggest (OpenAI)</button>
              <button class="secondary small" disabled={busy()} onClick={() => suggest("google")}>Suggest (Google)</button>
              <span class="muted small">v{props.unit.version}</span>
            </div>
          </Show>
        </div>
      </Show>
    </div>
  );
}
