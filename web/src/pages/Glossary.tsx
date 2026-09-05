import { For, Show, createResource, createSignal } from "solid-js";
import { useParams, useSearchParams } from "@solidjs/router";
import { api, fmtTime, type GlossaryTerm } from "../api";
import { useAction, useSession } from "../session";
import { Crumbs, Empty, LocaleSelect, formData } from "../ui";

export function GlossaryPage() {
  const params = useParams();
  const id = () => Number(params.id);
  const [search, setSearch] = useSearchParams<{ locale?: string }>();
  const { locales, notify } = useSession();
  const run = useAction();
  const [detail] = createResource(id, api.project);
  const locale = () => (typeof search.locale === "string" && search.locale) || "";
  const [terms, { refetch }] = createResource(() => ({ id: id(), locale: locale() }), (p) => api.glossary(p.id, p.locale));
  const [editing, setEditing] = createSignal<GlossaryTerm | null>(null);
  const role = () => detail()?.role ?? "viewer";
  const canEdit = () => role() !== "viewer";
  const manage = () => role() === "manager" || role() === "admin";
  const [filter, setFilter] = createSignal("");
  const visible = () => {
    const q = filter().trim().toLowerCase();
    return (terms() ?? []).filter((t) => !q || t.term.toLowerCase().includes(q) || t.translation.toLowerCase().includes(q) || t.note.toLowerCase().includes(q));
  };
  const save = async (e: SubmitEvent) => {
    const { form, data } = formData(e);
    if (await run(() => api.saveGlossaryTerm(id(), { locale: data.locale, term: data.term, translation: data.translation, note: data.note }), "Term saved")) {
      form.reset();
      setEditing(null);
      refetch();
    }
  };
  const importCsv = async (e: SubmitEvent) => {
    const { form } = formData(e);
    const file = (form.elements.namedItem("file") as HTMLInputElement).files?.[0];
    const loc = (form.elements.namedItem("locale") as HTMLSelectElement).value;
    if (!file) return;
    const ok = await run(async () => {
      const r = await api.importGlossary(id(), file, loc || undefined);
      notify(`Imported ${r.imported} term${r.imported === 1 ? "" : "s"}${r.skipped ? `, skipped ${r.skipped} empty row(s)` : ""}`, true);
    });
    if (ok) { form.reset(); refetch(); }
  };
  const remove = async (t: GlossaryTerm) => {
    if (!confirm(`Delete "${t.term}" (${t.locale})?`)) return;
    if (await run(() => api.deleteGlossaryTerm(id(), t.id))) refetch();
  };
  return (
    <Show when={detail()} fallback={<Empty>Loading…</Empty>}>
      {(d) => (
        <>
          <Crumbs items={[{ href: "/projects", label: "Projects" }, { href: `/projects/${id()}`, label: d().project.name }, { label: "Glossary" }]} />
          <h1>Glossary</h1>
          <p class="muted small">Terms are matched case-insensitively inside source strings and shown to translators in the editor, together with a check that the translation contains the expected wording.</p>
          <div class="row center mb">
            <label>Locale
              <LocaleSelect locales={d().locales} value={locale()} allowEmpty="all locales" onChange={(v) => setSearch({ locale: v || undefined })} />
            </label>
            <input class="grow" type="search" placeholder="Filter terms" value={filter()} onInput={(e) => setFilter(e.currentTarget.value)} />
            <a class="small" href={api.glossaryExportUrl(id(), locale() || undefined)}>Export CSV{locale() ? ` (${locale()})` : ""}</a>
          </div>
          <Show when={canEdit()}>
            <details>
              <summary>Import CSV</summary>
              <form class="panel row" onSubmit={importCsv}>
                <label>File<input name="file" type="file" accept=".csv,text/csv" required /></label>
                <label>Locale for rows without a locale column
                  <LocaleSelect name="locale" locales={d().locales} value={locale()} allowEmpty="use the locale column" />
                </label>
                <button type="submit" class="secondary">Import</button>
                <span class="muted small">Header row required: <code>term,translation</code> plus optional <code>locale</code> and <code>note</code>. Existing terms are updated.</span>
              </form>
            </details>
          </Show>
          <Show when={canEdit()}>
            <form class="panel row" onSubmit={save}>
              <label>Locale<LocaleSelect name="locale" locales={d().locales} value={editing()?.locale || locale() || d().locales[0]?.code} /></label>
              <label>Term ({d().project.source_locale})<input name="term" required maxlength="200" value={editing()?.term ?? ""} /></label>
              <label>Translation<input name="translation" required maxlength="500" value={editing()?.translation ?? ""} /></label>
              <label class="grow">Note<input name="note" maxlength="1000" value={editing()?.note ?? ""} placeholder="usage, context, forbidden alternatives…" /></label>
              <button type="submit">{editing() ? "Update" : "Add term"}</button>
              <Show when={editing()}><button type="button" class="secondary" onClick={() => setEditing(null)}>Cancel</button></Show>
            </form>
          </Show>
          <Show when={visible().length} fallback={<Empty>{terms()?.length ? "No terms match the filter." : "No glossary terms yet."}</Empty>}>
            <div class="table-wrap">
              <table>
                <thead><tr><th>Term</th><th>Locale</th><th>Translation</th><th>Note</th><th>Updated</th><th /></tr></thead>
                <tbody>
                  <For each={visible()}>
                    {(t) => (
                      <tr>
                        <td><strong>{t.term}</strong></td>
                        <td><span class="badge">{t.locale}</span></td>
                        <td>{t.translation}</td>
                        <td class="small muted">{t.note}</td>
                        <td class="small muted">{fmtTime(t.updated_at)}<br />{t.updated_by_name}</td>
                        <td class="right">
                          <Show when={canEdit()}><button class="ghost small" onClick={() => setEditing(t)}>Edit</button></Show>
                          <Show when={manage()}><button class="ghost small" onClick={() => remove(t)}>Delete</button></Show>
                        </td>
                      </tr>
                    )}
                  </For>
                </tbody>
              </table>
            </div>
          </Show>
        </>
      )}
    </Show>
  );
}
