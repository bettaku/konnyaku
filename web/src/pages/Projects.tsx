import { For, Show, createResource } from "solid-js";
import { A } from "@solidjs/router";
import { api } from "../api";
import { useAction, useSession } from "../session";
import { Empty, LocaleSelect, formData } from "../ui";

export function ProjectsPage() {
  const { user, locales } = useSession();
  const run = useAction();
  const [projects, { refetch }] = createResource(() => api.projects());
  const create = async (e: SubmitEvent) => {
    const { form, data } = formData(e);
    if (await run(() => api.createProject({ slug: data.slug, name: data.name, source_locale: data.source_locale }), "Project created")) {
      form.reset();
      refetch();
    }
  };
  return (
    <>
      <h1>Projects</h1>
      <Show when={projects()?.length} fallback={<Empty>No projects yet.</Empty>}>
        <div class="table-wrap">
          <table>
            <thead><tr><th>Project</th><th>Slug</th><th>Source locale</th></tr></thead>
            <tbody>
              <For each={projects()}>
                {(p) => (
                  <tr>
                    <td><A href={`/projects/${p.id}`}>{p.name}</A></td>
                    <td><code>{p.slug}</code></td>
                    <td>{p.source_locale}</td>
                  </tr>
                )}
              </For>
            </tbody>
          </table>
        </div>
      </Show>
      <Show when={user()?.admin}>
        <form class="panel row" onSubmit={create}>
          <label>Slug<input name="slug" required pattern="[a-z0-9][a-z0-9_-]{0,63}" /></label>
          <label class="grow">Name<input name="name" required /></label>
          <label>Source locale<LocaleSelect name="source_locale" locales={locales()} /></label>
          <button type="submit" disabled={!locales().length}>Create project</button>
          <Show when={!locales().length}><span class="muted small">Add a <A href="/locales">locale</A> first.</span></Show>
        </form>
      </Show>
    </>
  );
}
