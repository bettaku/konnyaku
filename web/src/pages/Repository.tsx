import { For, Show, createResource, createSignal } from "solid-js";
import { A, useNavigate, useParams } from "@solidjs/router";
import { api, type Candidate } from "../api";
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
  const manage = () => project()?.role === "manager" || project()?.role === "admin";

  const action = async (name: "clone" | "pull" | "push" | "sync" | "commit", message = "") => {
    setBusy(name);
    try {
      const r = await api.repositoryAction(id(), name, message);
      if (name === "sync") {
        const parts = Object.entries(r.imported ?? {}).map(([k, v]) => `${k}: ${v}`);
        notify(parts.length ? `Imported ${parts.join(", ")}` : "Nothing to import (attach components first)", true);
      } else if (name === "commit") notify(r.committed ? "Committed translation changes" : "No translation changes to commit", true);
      else notify(`${name} done`, true);
      refetch();
    } catch (err) {
      notify(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy("");
    }
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
    const ok = await run(() => api.createComponent(status()!.repository.project_id, { slug: data.slug, name: data.name, format: c.format, repository_id: id(), file_pattern: c.pattern }), "Component created");
    if (ok) { setCandidates((cs) => cs?.filter((x) => x !== c) ?? null); refetch(); }
  };
  const pullRequest = async (e: SubmitEvent) => {
    const { data } = formData(e);
    setBusy("pr");
    try {
      const r = await api.pullRequest(id(), data.title, data.body);
      notify(`Pull request opened: ${r.url}`, true);
      refetch();
    } catch (err) {
      notify(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy("");
    }
  };
  const remove = async () => {
    if (!confirm("Disconnect this repository and delete its checkout? Components stay, but lose their repository link.")) return;
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
                <dt>Checkout</dt>
                <dd>
                  <Show when={s().checkout.exists} fallback={<span class="muted">not cloned yet</span>}>
                    <code>{s().checkout.branch}</code> @ <code>{s().checkout.commit}</code> {s().checkout.subject}
                    <Show when={s().checkout.dirty}><div class="error small">{s().checkout.modified} uncommitted change(s) in the checkout</div></Show>
                  </Show>
                </dd>
                <dt>GitHub token</dt><dd>{s().github_token ? "configured" : <span class="error">not configured (push and pull requests unavailable)</span>}</dd>
              </dl>
            </section>
            <section class="panel">
              <h3>Actions</h3>
              <Show when={manage()} fallback={<p class="muted">Managers can synchronize; administrators can push and open pull requests.</p>}>
                <div class="row">
                  <Show when={!s().checkout.exists} fallback={<button class="secondary" disabled={!!busy() || !user()?.admin} onClick={() => action("pull")}>Pull</button>}>
                    <button disabled={!!busy() || !user()?.admin} onClick={() => action("clone")}>Clone</button>
                  </Show>
                  <button class="secondary" disabled={!!busy() || !s().checkout.exists} onClick={() => action("sync")} title="Import source and target files for attached components">Sync from checkout</button>
                  <button class="secondary" disabled={!!busy() || !s().checkout.exists} onClick={scan}>Detect translation files</button>
                </div>
                <Show when={user()?.admin && s().checkout.exists}>
                  <h3 class="mt">Publish translations</h3>
                  <form class="row" onSubmit={pullRequest}>
                    <label class="grow">Pull request title<input name="title" placeholder="Update translations" /></label>
                    <label class="grow">Body<input name="body" placeholder="optional" /></label>
                    <button type="submit" disabled={!!busy() || !s().github_token}>Open draft pull request</button>
                  </form>
                  <p class="muted small">Exports every target locale on a new <code>konnyaku/translations-…</code> branch, pushes it and opens a draft PR against <code>{s().repository.branch}</code>.</p>
                  <details>
                    <summary>Commit directly to {s().repository.branch}</summary>
                    <form class="row" onSubmit={(e) => { const { data } = formData(e); action("commit", data.message); }}>
                      <label class="grow">Commit message<input name="message" placeholder="Update translations" /></label>
                      <button type="submit" class="secondary" disabled={!!busy()}>Export & commit</button>
                      <button type="button" class="secondary" disabled={!!busy()} onClick={() => action("push")}>Push</button>
                    </form>
                  </details>
                  <div class="mt"><button class="danger small" onClick={remove}>Disconnect repository</button></div>
                </Show>
              </Show>
              <Show when={busy()}><p class="muted small">Running {busy()}…</p></Show>
            </section>
          </div>

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
                              locales: {c.locales.join(", ")}
                              <Show when={unknownLocales(c).length}> — <span class="error">not defined: {unknownLocales(c).join(", ")}</span> (<A href="/locales">add locales</A>)</Show>
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
