import { For, Show, createResource, createSignal } from "solid-js";
import { useNavigate, useParams } from "@solidjs/router";
import { A } from "@solidjs/router";
import { api, type Candidate, type SyncResult, type Publication } from "../api";
import { diffWords } from "../diff";
import { useAction, useSession } from "../session";
import { Crumbs, Empty, formData } from "../ui";

export function RepositoryPage() {
  const params = useParams();
  const id = () => Number(params.id);
  const { user, locales, notify } = useSession();
  const run = useAction();
  const navigate = useNavigate();
  const [status, { refetch }] = createResource(id, api.repository);
  const [project] = createResource(() => status()?.repository.project_id ?? null, api.project);
  const [candidates, setCandidates] = createSignal<Candidate[] | null>(null);
  const [busy, setBusy] = createSignal("");
  const [preview, setPreview] = createSignal<Publication | null>(null);
  const [lastSync, setLastSync] = createSignal<SyncResult | null>(null);
  const manage = () => project()?.role === "manager" || project()?.role === "admin";

  const sync = async () => {
    setBusy("sync");
    try {
      const r = await api.syncRepository(id());
      setLastSync(r.sync);
      setPreview(null);
      const total = r.sync.files.reduce((n, f) => n + f.imported, 0);
      const unchanged = r.sync.files.filter((f) => f.unchanged).length;
      notify(`Imported ${total} entries; ${unchanged} unchanged file(s); ${r.sync.errors.length} error(s)`, !r.sync.errors.length);
      refetch();
    } catch (err) { notify(err instanceof Error ? err.message : String(err)); }
    finally { setBusy(""); }
  };
  const scan = async () => {
    setBusy("scan");
    try {
      setCandidates(await api.scanRepository(id()));
    } catch (err) {
      notify(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy("");
    }
  };
  const createFromCandidate = async (c: Candidate, e: SubmitEvent) => {
    const { data } = formData(e);
    const ok = await run(() => api.createComponent(status()!.repository.project_id, { slug: data.slug, name: data.name, format: c.format, repository_id: id(), file_pattern: c.pattern }));
    if (!ok) return;
    setCandidates((cs) => cs?.filter((x) => x !== c) ?? null);
    // Import the files right away so progress reflects the existing translations.
    await sync();
  };
  const previewChanges = async (e: SubmitEvent) => {
    const { data } = formData(e);
    setBusy("preview");
    setPreview(null);
    try { setPreview(await api.previewRepository(id(), data.title, data.body)); refetch(); }
    catch (err) { notify(err instanceof Error ? err.message : String(err)); }
    finally { setBusy(""); }
  };
  const resume = async () => {
    const job = status()?.publication;
    if (!job) return;
    setBusy("preview");
    try { setPreview(await api.publication(id(), job.id)); }
    catch (err) { notify(err instanceof Error ? err.message : String(err)); }
    finally { setBusy(""); }
  };
  const pullRequest = async () => {
    const job = preview();
    if (!job) return;
    setBusy("pr");
    try {
      const r = await api.pullRequest(id(), job.id);
      setPreview(r);
      notify(`Pull request opened: ${r.url}`, true);
      refetch();
    } catch (err) { notify(err instanceof Error ? err.message : String(err)); refetch(); }
    finally { setBusy(""); }
  };
  const remove = async () => {
    if (!confirm("Disconnect this repository? Components stay, but lose their repository link.")) return;
    if (await run(() => api.deleteRepository(id()))) navigate(`/projects/${status()!.repository.project_id}`);
  };
  const suggestSlug = (pattern: string) => pattern.replace(/\{locale\}/g, "").replace(/\.[a-z]+$/i, "").replace(/[^a-z0-9]+/gi, "-").replace(/^-+|-+$/g, "").toLowerCase().slice(0, 64) || "strings";
  const unknownLocales = (c: Candidate) => c.locales.filter((l) => !locales().some((x) => x.code === l));

  return (
    <Show when={status()} fallback={<Empty>Loading…</Empty>}>
      {(s) => (
        <>
          <Crumbs items={[{ href: "/projects", label: "Projects" }, { href: `/projects/${s().repository.project_id}`, label: project()?.project.name ?? "Project" }, { label: s().repository.name }]} />
          <h1>{s().repository.name}</h1>
          <div class="grid2">
            <section class="panel">
              <h3>Repository</h3>
              <dl class="kv">
                <dt>URL</dt><dd><a href={s().repository.url.replace(/\.git$/, "")} target="_blank" rel="noopener">{s().repository.url}</a></dd>
                <dt>Tracked branch</dt><dd><code>{s().repository.branch}</code></dd>
                <dt>Last imported commit</dt><dd><code>{s().last_import_commit || "Not synchronized yet"}</code></dd>
                <dt>Last file import</dt><dd>{s().synced_at ? new Date(s().synced_at!).toLocaleString() : "—"}</dd>
                <dt>GitHub token</dt><dd>{s().github_token ? "configured" : <span class="muted">not configured (public repositories can be read; publishing is unavailable)</span>}</dd>
              </dl>
            </section>
            <section class="panel">
              <h3>Actions</h3>
              <Show when={manage()} fallback={<p class="muted">Managers can synchronize; administrators can publish pull requests.</p>}>
                <div class="row">
                  <button class="secondary" disabled={!!busy()} onClick={sync}>Sync from GitHub</button>
                  <button class="secondary" disabled={!!busy()} onClick={scan}>Detect translation files</button>
                </div>
                <Show when={user()?.admin}>
                  <h3 class="mt">Publish translations</h3>
                  <form class="row" onSubmit={previewChanges}>
                    <label class="grow">Pull request title<input name="title" placeholder="Update translations" /></label>
                    <label class="grow">Body<input name="body" placeholder="optional" /></label>
                    <button type="submit" disabled={!!busy()}>Preview changes</button>
                  </form>
                  <p class="muted small">Review the exported translations, then open a draft pull request against <code>{s().repository.branch}</code>.</p>
                  <Show when={s().publication}>
                    {(p) => <Show when={p().url} fallback={<button class="secondary" disabled={!!busy()} onClick={resume}>Review / resume previous publication</button>}>
                      <a href={p().url} target="_blank" rel="noopener">View latest pull request</a>
                    </Show>}
                  </Show>
                  <div class="mt"><button class="danger small" disabled={!!busy()} onClick={remove}>Disconnect repository</button></div>
                </Show>
              </Show>
              <Show when={busy()}><p class="muted small">Running {busy()}…</p></Show>
            </section>
          </div>

          <Show when={preview()}>
            {(p) => <section class="panel">
              <h2>{p().title}</h2>
              <p>{p().body}</p>
              <Show when={p().url} fallback={<>
                <p class="muted small">This preview is saved. Later translation edits are excluded; generate a new preview to include them.</p>
                <For each={p().changes ?? []}>{(change) => <details>
                  <summary><code>{change.path}</code></summary>
                  <pre class="publication-diff"><For each={diffWords(change.before, change.content)}>{(part) =>
                    part.type === "add" ? <ins>{part.text}</ins> : part.type === "del" ? <del>{part.text}</del> : <span>{part.text}</span>
                  }</For></pre>
                </details>}</For>
                <button class="mt" disabled={!!busy() || !s().github_token} onClick={pullRequest}>Open draft pull request</button>
              </>}><a href={p().url} target="_blank" rel="noopener">View pull request</a></Show>
            </section>}
          </Show>

          <Show when={lastSync()}>
            {(r) => (
              <>
                <h2>Sync report</h2>
                <Show when={r().errors.length}>
                  <div class="panel">
                    <strong class="error">Files that could not be imported</strong>
                    <ul class="small"><For each={r().errors}>{(e) => <li>{e}</li>}</For></ul>
                  </div>
                </Show>
                <Show when={r().files.length}>
                  <div class="table-wrap">
                    <table>
                      <thead><tr><th>Component</th><th>Locale</th><th>File</th><th>Imported</th><th>Unknown keys</th><th>Empty</th></tr></thead>
                      <tbody>
                        <For each={r().files}>
                          {(f) => (
                            <tr>
                              <td>{f.component}</td>
                              <td><span class="badge">{f.locale}</span></td>
                              <td><code>{f.path}</code></td>
                              <td>{f.unchanged ? "Unchanged" : f.imported}</td>
                              <td class={f.unknown ? "term-missing" : "muted"} title="keys present in this file but not in the source catalog; skipped">{f.unknown}</td>
                              <td class="muted">{f.empty}</td>
                            </tr>
                          )}
                        </For>
                      </tbody>
                    </table>
                  </div>
                </Show>
                <Show when={r().ignored.length}>
                  <p class="muted small">Ignored (name is not a locale): {r().ignored.map((p) => <><code>{p}</code> </>)}</p>
                </Show>
              </>
            )}
          </Show>
          <Show when={candidates()}>
            {(cs) => (
              <>
                <h2>Detected translation files</h2>
                <Show when={cs().length} fallback={<Empty>No files named after locale codes were found (looked for *.json, *.yaml, *.po and Android strings.xml).</Empty>}>
                  <For each={cs()}>
                    {(c) => {
                      const attached = () => s().components.some((x) => x.file_pattern === c.pattern);
                      return (
                        <form class="panel row" onSubmit={[createFromCandidate, c] as unknown as (e: SubmitEvent) => void}>
                          <div class="grow">
                            <code>{c.pattern}</code> <span class="badge">{c.format}</span>
                            <div class="small muted">
                              locales in repository: {c.locales.join(", ")}
                              <Show when={unknownLocales(c).length}> — {unknownLocales(c).join(", ")} will be registered on sync</Show>
                            </div>
                          </div>
                          <Show when={!attached()} fallback={<span class="badge reviewed">attached</span>}>
                            <label>Slug<input name="slug" value={suggestSlug(c.pattern)} required pattern="[a-z0-9][a-z0-9_-]{0,63}" /></label>
                            <label>Name<input name="name" value={suggestSlug(c.pattern)} required /></label>
                            <button type="submit" disabled={!manage()}>Create component</button>
                          </Show>
                        </form>
                      );
                    }}
                  </For>
                </Show>
              </>
            )}
          </Show>

          <h2>Attached components</h2>
          <Show when={s().components.length} fallback={<Empty>No components use this repository yet. Detect translation files above, or set the repository on a component.</Empty>}>
            <div class="table-wrap">
              <table>
                <thead><tr><th>Component</th><th>Format</th><th>File pattern</th></tr></thead>
                <tbody>
                  <For each={s().components}>{(c) => <tr><td><A href={`/components/${c.id}`}>{c.name}</A></td><td>{c.format}</td><td><code>{c.file_pattern}</code></td></tr>}</For>
                </tbody>
              </table>
            </div>
          </Show>
        </>
      )}
    </Show>
  );
}
