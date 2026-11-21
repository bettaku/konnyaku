import { For, Show, createMemo, createResource, createSignal } from "solid-js";
import { A, useNavigate, useParams, useSearchParams } from "@solidjs/router";
import { api, fmtTime, percent, type Format, type Stat } from "../api";
import { useAction, useSession } from "../session";
import { Badge, Crumbs, Empty, Legend, LocaleSelect, Progress, formData } from "../ui";

type SortKey = "code" | "progress" | "needs";
type LocaleRow = { code: string; name: string; total: Stat; byComponent: Map<number, Stat> };

const emptyStat = (locale: string): Stat => ({ locale, total: 0, translated: 0, reviewed: 0, needs_review: 0 });
const num = (n: number) => n.toLocaleString();

export function ProjectPage() {
  const params = useParams();
  const id = () => Number(params.id);
  const [search, setSearch] = useSearchParams<{ tab?: string }>();
  const { user, locales } = useSession();
  const run = useAction();
  const navigate = useNavigate();
  const [detail, { refetch: refetchDetail }] = createResource(id, api.project);
  const [components, { refetch: refetchComponents }] = createResource(id, api.components);
  const [repositories, { refetch: refetchRepos }] = createResource(id, api.repositories);
  const [stats, { refetch: refetchStats }] = createResource(id, api.projectStats);
  const [history] = createResource(id, api.projectHistory);
  const [issueCounts] = createResource(id, api.projectIssues);
  const issuesFor = (componentId: number) => issueCounts()?.find((x) => x.component_id === componentId)?.issues ?? 0;
  const manage = () => detail()?.role === "manager" || detail()?.role === "admin";
  const [members, { refetch: refetchMembers }] = createResource(() => (manage() ? id() : null), api.members);
  const [users] = createResource(() => (user()?.admin && manage() ? true : null), () => api.users());
  const tab = () => (search.tab === "settings" && manage() ? "settings" : "overview");

  const targetLocales = () => detail()?.locales ?? [];
  const repoFor = (rid: number) => repositories()?.find((r) => r.id === rid);
  const multi = () => (components()?.length ?? 0) > 1;

  // One row per target locale with per-component stats and the locale total.
  const rows = createMemo<LocaleRow[]>(() =>
    targetLocales().map((l) => {
      const byComponent = new Map<number, Stat>();
      const total = emptyStat(l.code);
      for (const s of stats() ?? []) {
        if (s.locale !== l.code) continue;
        byComponent.set(s.component_id, s);
        total.total += s.total; total.translated += s.translated; total.reviewed += s.reviewed; total.needs_review += s.needs_review;
      }
      return { code: l.code, name: l.name, total, byComponent };
    }),
  );
  const overall = createMemo(() => {
    const t = emptyStat("");
    for (const r of rows()) { t.total += r.total.total; t.translated += r.total.translated; t.reviewed += r.total.reviewed; t.needs_review += r.total.needs_review; }
    return t;
  });

  const [filter, setFilter] = createSignal("");
  const [sort, setSort] = createSignal<{ key: SortKey; asc: boolean }>({ key: "code", asc: true });
  const toggleSort = (key: SortKey) => setSort((s) => (s.key === key ? { key, asc: !s.asc } : { key, asc: key === "code" || key === "progress" }));
  const sortMark = (key: SortKey) => (sort().key === key ? (sort().asc ? " ▲" : " ▼") : "");
  const visibleRows = createMemo(() => {
    const q = filter().trim().toLowerCase();
    const { key, asc } = sort();
    const val = (r: LocaleRow) => (key === "progress" ? percent(r.total) : key === "needs" ? r.total.needs_review : 0);
    return rows()
      .filter((r) => !q || r.code.toLowerCase().includes(q) || r.name.toLowerCase().includes(q))
      .sort((a, b) => {
        const d = key === "code" ? a.code.localeCompare(b.code) : val(a) - val(b) || a.code.localeCompare(b.code);
        return asc ? d : -d;
      });
  });
  // With a single component, a locale row links straight into its editor.
  const editorHref = (componentId: number, locale: string) => `/components/${componentId}?locale=${encodeURIComponent(locale)}`;

  const addComponent = async (e: SubmitEvent) => {
    const { form, data } = formData(e);
    const ok = await run(() => api.createComponent(id(), {
      slug: data.slug, name: data.name, format: data.format as Format,
      repository_id: data.repository_id ? Number(data.repository_id) : null, file_pattern: data.file_pattern,
    }), "Component created");
    if (ok) { form.reset(); refetchComponents(); refetchStats(); }
  };
  const addRepository = async (e: SubmitEvent) => {
    const { form, data } = formData(e);
    if (await run(() => api.createRepository(id(), { url: data.url, branch: data.branch, name: data.name }), "Repository added")) { form.reset(); refetchRepos(); }
  };
  const addLocale = async (e: SubmitEvent) => {
    const { form, data } = formData(e);
    if (data.locale && (await run(() => api.addProjectLocale(id(), data.locale)))) { form.reset(); refetchDetail(); refetchStats(); }
  };
  const removeLocale = async (code: string) => {
    if (!confirm(`Stop tracking ${code}? Existing translations are kept.`)) return;
    if (await run(() => api.removeProjectLocale(id(), code))) { refetchDetail(); refetchStats(); }
  };
  const saveMember = async (e: SubmitEvent) => {
    const { data } = formData(e);
    if (await run(() => api.saveMember(id(), Number(data.user_id), data.role), "Member saved")) refetchMembers();
  };
  const removeMember = async (userId: number, name: string) => {
    if (!confirm(`Remove ${name} from this project?`)) return;
    if (await run(() => api.deleteMember(id(), userId))) refetchMembers();
  };
  const rename = async (e: SubmitEvent) => {
    const { data } = formData(e);
    if (await run(() => api.renameProject(id(), data.name), "Renamed")) refetchDetail();
  };
  const remove = async () => {
    const p = detail()?.project;
    if (!p || !confirm(`Delete project "${p.name}" with all components and translations?`)) return;
    if (await run(() => api.deleteProject(id()))) navigate("/projects");
  };

  return (
    <Show when={detail()} fallback={<Empty>Loading…</Empty>}>
      {(d) => (
        <>
          <Crumbs items={[{ href: "/projects", label: "Projects" }, { label: d().project.name }]} />
          <div class="row center">
            <h1 class="m0">{d().project.name}</h1>
            <span class="badge">source {d().project.source_locale}</span>
            <span class="badge">{d().role}</span>
            <A class="small" href={`/projects/${id()}/glossary`}>Glossary</A>
          </div>

          <div class="tabs mt">
            <button class={"tab" + (tab() === "overview" ? " active" : "")} onClick={() => setSearch({ tab: undefined })}>Overview</button>
            <Show when={manage()}>
              <button class={"tab" + (tab() === "settings" ? " active" : "")} onClick={() => setSearch({ tab: "settings" })}>Settings</button>
            </Show>
          </div>

          <Show when={tab() === "overview"}>
            <div class="stats">
              <div class="stat"><span class="num">{percent(overall())}%</span><span class="muted small">translated overall</span></div>
              <div class="stat"><span class="num">{targetLocales().length}</span><span class="muted small">target locales</span></div>
              <div class="stat"><span class="num">{num(overall().total - overall().translated)}</span><span class="muted small">untranslated strings</span></div>
              <div class="stat"><span class="num">{num(overall().needs_review)}</span><span class="muted small">need review</span></div>
            </div>

            <div class="row center between mt">
              <h2 class="m0">Progress</h2>
              <div class="row center">
                <Legend />
                <Show when={targetLocales().length > 8}>
                  <input class="filter" type="search" placeholder="Filter locales…" value={filter()} onInput={(e) => setFilter(e.currentTarget.value)} />
                </Show>
              </div>
            </div>
            <div class="table-wrap">
              <table class="progress-table">
                <thead>
                  <tr>
                    <th><button class="th-sort" onClick={() => toggleSort("code")}>Locale{sortMark("code")}</button></th>
                    <Show when={multi()}>
                      <For each={components()}>
                        {(c) => (
                          <th>
                            <A href={`/components/${c.id}`}>{c.name}</A>
                            <Show when={issuesFor(c.id)}>{(n) => <span class="badge needs_review ml" title="keys in translation files that the source catalog lacks">⚠ {n()}</span>}</Show>
                          </th>
                        )}
                      </For>
                    </Show>
                    <th class="w-progress"><button class="th-sort" onClick={() => toggleSort("progress")}>{multi() ? "All components" : "Progress"}{sortMark("progress")}</button></th>
                    <th class="right"><button class="th-sort" onClick={() => toggleSort("needs")}>Needs review{sortMark("needs")}</button></th>
                    <Show when={manage()}><th /></Show>
                  </tr>
                </thead>
                <tbody>
                  <For each={visibleRows()} fallback={<tr><td colspan={99}><Empty>{targetLocales().length ? "No locales match the filter." : "No target locales yet."}</Empty></td></tr>}>
                    {(r) => (
                      <tr>
                        <td>
                          <Show when={!multi() && components()?.[0]} fallback={<code>{r.code}</code>}>
                            {(c) => <A href={editorHref(c().id, r.code)}><code>{r.code}</code></A>}
                          </Show>
                          <span class="muted small ml">{r.name}</span>
                        </td>
                        <Show when={multi()}>
                          <For each={components()}>
                            {(c) => <td><A href={editorHref(c.id, r.code)}><Progress stat={r.byComponent.get(c.id)} compact /></A></td>}
                          </For>
                        </Show>
                        <td><Progress stat={r.total} /></td>
                        <td class="right num-cell">
                          <Show when={r.total.needs_review} fallback={<span class="muted">—</span>}>{num(r.total.needs_review)}</Show>
                        </td>
                        <Show when={manage()}>
                          <td class="right"><button class="ghost small" title={`Stop tracking ${r.code}`} onClick={() => removeLocale(r.code)}>×</button></td>
                        </Show>
                      </tr>
                    )}
                  </For>
                </tbody>
                <Show when={manage()}>
                  <tfoot>
                    <tr>
                      <td colspan={99}>
                        <form class="row center" onSubmit={addLocale}>
                          <LocaleSelect name="locale" locales={locales()} exclude={[d().project.source_locale, ...targetLocales().map((l) => l.code)]} allowEmpty="Add a target locale…" />
                          <button type="submit" class="secondary small">Add</button>
                        </form>
                      </td>
                    </tr>
                  </tfoot>
                </Show>
              </table>
            </div>

            <div class="grid2">
              <section>
                <h2>Components</h2>
                <Show when={components()?.length} fallback={<Empty>No components yet.{manage() ? " Add one under Settings." : ""}</Empty>}>
                  <div class="table-wrap">
                    <table>
                      <thead><tr><th>Name</th><th>Format</th><th>Source</th></tr></thead>
                      <tbody>
                        <For each={components()}>
                          {(c) => (
                            <tr>
                              <td><A href={`/components/${c.id}`}>{c.name}</A>
                                <Show when={issuesFor(c.id)}>{(n) => <A href={`/components/${c.id}`} class="badge needs_review ml" title="keys in translation files that the source catalog lacks">⚠ {n()} unknown key{n() === 1 ? "" : "s"}</A>}</Show>
                              </td>
                              <td>{c.format}</td>
                              <td>
                                <Show when={c.repository_id} fallback={<span class="muted">manual upload</span>}>
                                  {(rid) => (
                                    <>
                                      <A href={`/repositories/${rid()}`}>{repoFor(rid())?.name ?? `#${rid()}`}</A>
                                      <Show when={repoFor(rid())?.branch}>{(b) => <> @ <code>{b()}</code></>}</Show>
                                      <div><code class="muted">{c.file_pattern}</code></div>
                                    </>
                                  )}
                                </Show>
                              </td>
                            </tr>
                          )}
                        </For>
                      </tbody>
                    </table>
                  </div>
                </Show>
              </section>

              <section>
                <h2>Recent activity</h2>
                <Show when={history()?.length} fallback={<Empty>No changes recorded yet.</Empty>}>
                  <ul class="activity panel list scroll">
                    <For each={history()}>
                      {(h) => (
                        <li>
                          <span class="meta">{fmtTime(h.changed_at)}<br />{h.changed_by_name || "system"}</span>
                          <span>
                            <A href={`/components/${h.component_id}?locale=${encodeURIComponent(h.locale)}&q=${encodeURIComponent(h.key)}`}>{h.component_name}</A>
                            {" "}<code>{h.key}</code> <span class="badge">{h.locale}</span> <Badge status={h.status} /> <span class="muted small">v{h.version}</span>
                            <div class="value">{h.value}</div>
                          </span>
                        </li>
                      )}
                    </For>
                  </ul>
                </Show>
              </section>
            </div>
          </Show>

          <Show when={tab() === "settings"}>
            <h2>General</h2>
            <form class="panel row" onSubmit={rename}>
              <label class="grow">Project name<input name="name" value={d().project.name} required /></label>
              <button type="submit" class="secondary">Rename</button>
            </form>

            <h2>Components</h2>
            <Show when={components()?.length} fallback={<Empty>No components yet.</Empty>}>
              <div class="table-wrap">
                <table>
                  <thead><tr><th>Name</th><th>Format</th><th>Repository</th><th>Pattern</th></tr></thead>
                  <tbody>
                    <For each={components()}>
                      {(c) => (
                        <tr>
                          <td><A href={`/components/${c.id}`}>{c.name}</A></td>
                          <td>{c.format}</td>
                          <td>
                            <Show when={c.repository_id} fallback={<span class="muted">—</span>}>
                              {(rid) => <A href={`/repositories/${rid()}`}>{repoFor(rid())?.name ?? `#${rid()}`}</A>}
                            </Show>
                          </td>
                          <td><code>{c.file_pattern}</code></td>
                        </tr>
                      )}
                    </For>
                  </tbody>
                </table>
              </div>
            </Show>
            <form class="panel row" onSubmit={addComponent}>
              <label>Slug<input name="slug" required pattern="[a-z0-9][a-z0-9_-]{0,63}" /></label>
              <label>Name<input name="name" required /></label>
              <label>Format
                <select name="format"><option>json</option><option>yaml</option><option>po</option><option>android</option></select>
              </label>
              <label>Repository
                <select name="repository_id">
                  <option value="">none (manual upload)</option>
                  <For each={repositories()}>{(r) => <option value={r.id}>{r.name}</option>}</For>
                </select>
              </label>
              <label class="grow">File pattern<input name="file_pattern" placeholder="locales/{locale}.json" /></label>
              <button type="submit">Add component</button>
            </form>

            <h2>Repositories</h2>
            <Show when={repositories()?.length} fallback={<Empty>No repositories connected.</Empty>}>
              <div class="table-wrap">
                <table>
                  <thead><tr><th>Name</th><th>Branch</th></tr></thead>
                  <tbody>
                    <For each={repositories()}>
                      {(r) => <tr><td><A href={`/repositories/${r.id}`}>{r.name}</A><div class="muted small">{r.url}</div></td><td><code>{r.branch}</code></td></tr>}
                    </For>
                  </tbody>
                </table>
              </div>
            </Show>
            <Show when={user()?.admin}>
              <form class="panel row" onSubmit={addRepository}>
                <label class="grow">GitHub URL<input name="url" required placeholder="https://github.com/owner/repo" /></label>
                <label>Branch<input name="branch" placeholder="main" /></label>
                <label>Name<input name="name" placeholder="optional" /></label>
                <button type="submit">Connect</button>
              </form>
            </Show>

            <h2>Members</h2>
            <Show when={members()?.length} fallback={<Empty>No members yet.</Empty>}>
              <div class="table-wrap">
                <table>
                  <thead><tr><th>User</th><th>Role</th><th /></tr></thead>
                  <tbody>
                    <For each={members()}>
                      {(m) => (
                        <tr>
                          <td>{m.name} <span class="muted small">{m.email}</span></td>
                          <td><Badge status={m.role} /></td>
                          <td class="right"><button class="danger small" onClick={() => removeMember(m.user_id, m.name)}>Remove</button></td>
                        </tr>
                      )}
                    </For>
                  </tbody>
                </table>
              </div>
            </Show>
            <form class="panel row" onSubmit={saveMember}>
              <label>User
                <Show when={users()} fallback={<input name="user_id" type="number" min="1" required placeholder="user ID" />}>
                  <select name="user_id"><For each={users()}>{(u) => <option value={u.id}>{u.name} ({u.email})</option>}</For></select>
                </Show>
              </label>
              <label>Role<select name="role"><option>viewer</option><option>translator</option><option>manager</option></select></label>
              <button type="submit">Add / update</button>
            </form>

            <h2>Danger zone</h2>
            <div class="panel row center between danger-zone">
              <span>Delete this project together with all of its components and translations.</span>
              <button class="danger" onClick={remove}>Delete project</button>
            </div>
          </Show>
        </>
      )}
    </Show>
  );
}
