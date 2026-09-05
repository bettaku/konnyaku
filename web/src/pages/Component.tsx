import { For, Show, createEffect, createMemo, createResource, createSignal, on } from "solid-js";
import { A, useNavigate, useParams, useSearchParams } from "@solidjs/router";
import { api, fmtTime, type Status, type Unit } from "../api";
import { useAction, useSession } from "../session";
import { Badge, Crumbs, Empty, LocaleSelect, Progress, formData } from "../ui";

const STATUSES: Array<{ value: string; label: string }> = [
  { value: "", label: "All" },
  { value: "untranslated", label: "Untranslated" },
  { value: "needs_review", label: "Needs review" },
  { value: "translated", label: "Translated" },
  { value: "reviewed", label: "Reviewed" },
];

export function ComponentPage() {
  const params = useParams();
  const id = () => Number(params.id);
  const [search, setSearch] = useSearchParams<{ locale?: string; q?: string; status?: string; offset?: string }>();
  const { user, locales, notify } = useSession();
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
  const [selected, setSelected] = createSignal<Unit | null>(null);
  const [history] = createResource(() => (selected() ? { u: selected()!.id, l: locale() } : null), (p) => api.unitHistory(p.u, p.l));
  const [activity, { refetch: refetchActivity }] = createResource(() => (locale() ? { id: id(), l: locale() } : null), (p) => api.componentHistory(p.id, p.l));
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
    mutate((p) => p && { ...p, units: p.units.map((x) => (x.id === u.id ? { ...x, value, status: st, version, updated_at: new Date().toISOString() } : x)) });
    refetchStats();
    refetchActivity();
    if (selected()?.id === u.id) setSelected({ ...u, value, status: st, version });
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
          <Show when={!isSource() && statFor(locale())}>{(s) => <div class="mb narrow"><Progress stat={s()} /></div>}</Show>

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
                  <Show when={activity()?.length} fallback={<p class="muted small">No changes yet. Select a unit to see its history.</p>}>
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
                  <div class="panel">
                    <div class="row center"><h3 class="grow m0">History</h3><button class="ghost small" onClick={() => setSelected(null)}>×</button></div>
                    <code class="small">{u().key}</code>
                    <Show when={history()} fallback={<p class="muted small">Loading…</p>}>
                      {(hs) => (
                        <Show when={hs().length} fallback={<p class="muted small">No history for this locale yet.</p>}>
                          <ul class="history list mt">
                            <For each={hs()}>
                              {(h) => (
                                <li>
                                  <div class="meta">v{h.version} · {fmtTime(h.changed_at)} · {h.changed_by_name || "system"} · <Badge status={h.status} /></div>
                                  <div class="value">{h.value || <span class="muted">(empty)</span>}</div>
                                </li>
                              )}
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

function UnitCard(props: {
  unit: Unit; locale: string; source: boolean; editable: boolean; reviewer: boolean; selected: boolean;
  onSelect: () => void; onSaved: (u: Unit, value: string, status: Status, version: number) => void;
}) {
  const { notify, user } = useSession();
  const [value, setValue] = createSignal(props.unit.value);
  const [status, setStatus] = createSignal<Status>(props.unit.status === "untranslated" ? "translated" : props.unit.status);
  const [busy, setBusy] = createSignal(false);
  createEffect(on(() => props.unit, (u) => { setValue(u.value); setStatus(u.status === "untranslated" ? "translated" : u.status); }));
  const dirty = () => value() !== props.unit.value || (props.unit.status !== "untranslated" && status() !== props.unit.status);
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
  void user;
  return (
    <div class={"unit" + (props.selected ? " selected" : "") + (dirty() ? " dirty" : "")}>
      <div class="head">
        <span class="key">{props.unit.key}</span>
        <Badge status={props.unit.status} />
        <Show when={props.unit.updated_at}><span class="muted small">{fmtTime(props.unit.updated_at)}</span></Show>
        <span class="grow" />
        <Show when={!props.source}><button class="ghost small" onClick={props.onSelect}>{props.selected ? "Hide history" : "History"}</button></Show>
      </div>
      <div class="source">{props.unit.source}</div>
      <Show when={!props.source} fallback={<div class="muted small">source string</div>}>
        <div>
          <textarea value={value()} disabled={!props.editable} onInput={(e) => setValue(e.currentTarget.value)}
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
