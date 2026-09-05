import { For, Show, createMemo, createResource, createSignal } from "solid-js";
import { A, useNavigate, useParams } from "@solidjs/router";
import { api, fmtTime, type Format, type ProjectStat, type Stat } from "../api";
import { useAction, useSession } from "../session";
import { Badge, Crumbs, Empty, Legend, LocaleSelect, Progress, formData } from "../ui";

export function ProjectPage() {
  const params = useParams();
  const id = () => Number(params.id);
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
  const [editing, setEditing] = createSignal(false);

  const targetLocales = () => detail()?.locales ?? [];
  const statFor = (componentId: number, locale: string): Stat | undefined => stats()?.find((s) => s.component_id === componentId && s.locale === locale);
  const totals = createMemo(() => {
    const m = new Map<string, ProjectStat>();
    for (const s of stats() ?? []) {
      const t = m.get(s.locale) ?? { component_id: 0, locale: s.locale, total: 0, translated: 0, reviewed: 0, needs_review: 0 };
      t.total += s.total; t.translated += s.translated; t.reviewed += s.reviewed; t.needs_review += s.needs_review;
      m.set(s.locale, t);
    }
    return [...m.values()];
  });

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
    const { data } = formData(e);
    if (data.locale && (await run(() => api.addProjectLocale(id(), data.locale)))) { refetchDetail(); refetchStats(); }
  };
  const removeLocale = async (code: string) => {
    if (!confirm(`Stop tracking ${code}? Existing translations are kept.`)) return;
    if (await run(() => api.removeProjectLocale(id(), code))) { refetchDetail(); refetchStats(); }
  };
  const saveMember = async (e: SubmitEvent) => {
    const { data } = formData(e);
    if (await run(() => api.saveMember(id(), Number(data.user_id), data.role), "Member saved")) refetchMembers();
  };
  const rename = async (e: SubmitEvent) => {
    const { data } = formData(e);
    if (await run(() => api.renameProject(id(), data.name), "Renamed")) { setEditing(false); refetchDetail(); }
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
            <Show when={manage()}><button class="ghost small" onClick={() => setEditing(!editing())}>Settings</button></Show>
          </div>
          <Show when={editing()}>
            <div class="panel row">
              <form class="row" onSubmit={rename}>
                <label>Name<input name="name" value={d().project.name} required /></label>
                <button type="submit" class="secondary">Rename</button>
              </form>
              <button class="danger" onClick={remove}>Delete project</button>
            </div>
          </Show>

          <h2>Progress</h2>
          <Show when={targetLocales().length} fallback={<Empty>No target locales yet. Add one below or import a translation file.</Empty>}>
            <div class="table-wrap">
              <table>
                <thead>
                  <tr>
                    <th>Component</th>
                    <For each={targetLocales()}>{(l) => <th>{l.code}</th>}</For>
                  </tr>
                </thead>
                <tbody>
                  <For each={components()}>
                    {(c) => (
                      <tr>
                        <td><A href={`/components/${c.id}`}>{c.name}</A> <span class="muted small">{c.format}</span></td>
                        <For each={targetLocales()}>
                          {(l) => <td><A href={`/components/${c.id}?locale=${encodeURIComponent(l.code)}`}><Progress stat={statFor(c.id, l.code)} compact /></A></td>}
                        </For>
                      </tr>
                    )}
                  </For>
                  <Show when={(components()?.length ?? 0) > 1}>
                    <tr>
                      <td><strong>All components</strong></td>
                      <For each={targetLocales()}>{(l) => <td><Progress stat={totals().find((t) => t.locale === l.code)} compact /></td>}</For>
                    </tr>
                  </Show>
                </tbody>
              </table>
            </div>
            <Legend />
          </Show>

          <div class="grid2">
            <section>
              <h2>Components</h2>
              <Show when={components()?.length} fallback={<Empty>No components yet.</Empty>}>
                <div class="table-wrap">
                  <table>
                    <thead><tr><th>Name</th><th>Format</th><th>Repository</th><th>Pattern</th></tr></thead>
                    <tbody>
                      <For each={components()}>
                        {(c) => (
                          <tr>
                            <td><A href={`/components/${c.id}`}>{c.name}</A>
                              <Show when={issuesFor(c.id)}>{(n) => <A href={`/components/${c.id}`} class="badge needs_review ml" title="keys in translation files that the source catalog lacks">⚠ {n()} unknown key{n() === 1 ? "" : "s"}</A>}</Show>
                            </td>
                            <td>{c.format}</td>
                            <td>
                              <Show when={c.repository_id} fallback={<span class="muted">—</span>}>
                                {(rid) => <A href={`/repositories/${rid()}`}>{repositories()?.find((r) => r.id === rid())?.name ?? `#${rid()}`}</A>}
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
              <Show when={manage()}>
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
                  <label>File pattern<input name="file_pattern" placeholder="locales/{locale}.json" /></label>
                  <button type="submit">Add component</button>
                </form>
              </Show>

              <h2>Target locales</h2>
              <div class="chips">
                <For each={targetLocales()}>
                  {(l) => (
                    <span class="chip">{l.code} · {l.name}
                      <Show when={manage()}> <button class="ghost small" title="Remove" onClick={() => removeLocale(l.code)}>×</button></Show>
                    </span>
                  )}
                </For>
              </div>
              <Show when={manage()}>
                <form class="row mt" onSubmit={addLocale}>
                  <label>Add locale
                    <LocaleSelect name="locale" locales={locales()} exclude={[d().project.source_locale, ...targetLocales().map((l) => l.code)]} allowEmpty="choose…" />
                  </label>
                  <button type="submit" class="secondary">Add</button>
                </form>
              </Show>
            </section>

            <section>
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

              <Show when={manage()}>
                <h2>Members</h2>
                <div class="table-wrap">
                  <table>
                    <thead><tr><th>User</th><th>Role</th><th /></tr></thead>
                    <tbody>
                      <For each={members()}>
                        {(m) => (
                          <tr>
                            <td>{m.name} <span class="muted small">{m.email}</span></td>
                            <td><Badge status={m.role} /></td>
                            <td class="right">
                              <button class="danger small" onClick={() => run(() => api.deleteMember(id(), m.user_id)).then(() => refetchMembers())}>Remove</button>
                            </td>
                          </tr>
                        )}
                      </For>
                    </tbody>
                  </table>
                </div>
                <form class="panel row" onSubmit={saveMember}>
                  <label>User
                    <Show when={users()} fallback={<input name="user_id" type="number" min="1" required placeholder="user ID" />}>
                      <select name="user_id"><For each={users()}>{(u) => <option value={u.id}>{u.name} ({u.email})</option>}</For></select>
                    </Show>
                  </label>
                  <label>Role<select name="role"><option>viewer</option><option>translator</option><option>manager</option></select></label>
                  <button type="submit">Add / update</button>
                </form>
              </Show>
            </section>
          </div>

          <h2>Recent activity</h2>
          <Show when={history()?.length} fallback={<Empty>No changes recorded yet.</Empty>}>
            <ul class="activity panel list">
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
        </>
      )}
    </Show>
  );
}
