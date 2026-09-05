import { For, Show } from "solid-js";
import { api } from "../api";
import { useAction, useSession } from "../session";
import { Empty, formData } from "../ui";

export function LocalesPage() {
  const { user, locales, refreshLocales } = useSession();
  const run = useAction();
  const save = async (e: SubmitEvent) => {
    const { form, data } = formData(e);
    if (await run(() => api.saveLocale(data.code, data.name), "Locale saved")) {
      form.reset();
      refreshLocales();
    }
  };
  const remove = async (code: string) => {
    if (!confirm(`Delete locale ${code}? This fails while any project uses it.`)) return;
    if (await run(() => api.deleteLocale(code))) refreshLocales();
  };
  return (
    <>
      <h1>Locales</h1>
      <Show when={locales().length} fallback={<Empty>No locales defined.</Empty>}>
        <div class="table-wrap">
          <table>
            <thead><tr><th>Code</th><th>Name</th><th /></tr></thead>
            <tbody>
              <For each={locales()}>
                {(l) => (
                  <tr>
                    <td><code>{l.code}</code></td>
                    <td>{l.name}</td>
                    <td class="right">
                      <Show when={user()?.admin}><button class="danger small" onClick={() => remove(l.code)}>Delete</button></Show>
                    </td>
                  </tr>
                )}
              </For>
            </tbody>
          </table>
        </div>
      </Show>
      <Show when={user()?.admin}>
        <form class="panel row" onSubmit={save}>
          <label>BCP 47 code (ja, pt-BR, zh-Hant)<input name="code" required /></label>
          <label class="grow">Name<input name="name" required /></label>
          <button type="submit">Save locale</button>
        </form>
      </Show>
    </>
  );
}
