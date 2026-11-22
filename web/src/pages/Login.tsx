import { For, createSignal } from "solid-js";
import { api } from "../api";
import { useSession } from "../session";
import { formData } from "../ui";

const features = [
  { title: "GitHub sync", body: "Import catalogs from your repository and publish translations as a draft pull request." },
  { title: "Review workflow", body: "Track translated, needs-review and reviewed strings per locale, with full history." },
  { title: "Memory & glossary", body: "Reuse past translations and keep terminology consistent across projects." },
  { title: "Machine translation", body: "Draft with an OpenAI-compatible model or Google Cloud Translation, then refine." },
];

// Purely decorative sample progress; widths come from .w-* classes because the CSP blocks inline styles.
const sample = [
  { code: "ja-JP", pct: 98, width: "w-98" },
  { code: "de-DE", pct: 85, width: "w-85" },
  { code: "pt-BR", pct: 62, width: "w-62" },
  { code: "ko-KR", pct: 34, width: "w-34" },
];

export function LoginPage() {
  const { setUser, refreshLocales } = useSession();
  const [error, setError] = createSignal("");
  const [busy, setBusy] = createSignal(false);
  const submit = async (e: SubmitEvent) => {
    const { data } = formData(e);
    setBusy(true);
    setError("");
    try {
      setUser(await api.login(data.email, data.password));
      refreshLocales();
    } catch (err) {
      setError(err instanceof Error ? err.message : "login failed");
    } finally {
      setBusy(false);
    }
  };
  return (
    <div class="landing">
      <section class="landing-hero">
        <p class="eyebrow">Translation management</p>
        <h1 class="landing-title">Ship every locale<br />from one place.</h1>
        <p class="landing-lead">
          Konnyaku keeps your app's translation files in sync with GitHub, so translators work in the browser
          and changes land back in your repository as reviewed pull requests.
        </p>

        <div class="landing-sample" aria-hidden="true">
          <div class="landing-sample-head">
            <span>Progress</span>
            <span class="muted small">4 locales</span>
          </div>
          <For each={sample}>
            {(s) => (
              <div class="landing-sample-row">
                <code>{s.code}</code>
                <div class="bar"><span class={"translated " + s.width} /></div>
                <span class="pct">{s.pct}%</span>
              </div>
            )}
          </For>
        </div>

        <ul class="landing-features list">
          <For each={features}>
            {(f) => (
              <li>
                <span class="landing-check" aria-hidden="true">✓</span>
                <div><strong>{f.title}</strong><div class="muted small">{f.body}</div></div>
              </li>
            )}
          </For>
        </ul>
      </section>

      <form class="panel login" onSubmit={submit}>
        <div>
          <h2 class="m0">Sign in</h2>
          <p class="muted small m0">Use the account your administrator created for you.</p>
        </div>
        <label>Email<input name="email" type="email" required autocomplete="username" /></label>
        <label>Password<input name="password" type="password" required autocomplete="current-password" /></label>
        {error() && <div class="error" role="alert">{error()}</div>}
        <button type="submit" disabled={busy()}>{busy() ? "Signing in…" : "Sign in"}</button>
      </form>
    </div>
  );
}
