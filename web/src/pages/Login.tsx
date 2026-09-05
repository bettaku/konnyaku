import { createSignal } from "solid-js";
import { api } from "../api";
import { useSession } from "../session";
import { formData } from "../ui";

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
    <form class="panel login" onSubmit={submit}>
      <h1>Sign in</h1>
      <label>Email<input name="email" type="email" required autocomplete="username" /></label>
      <label>Password<input name="password" type="password" required autocomplete="current-password" /></label>
      {error() && <div class="error">{error()}</div>}
      <button type="submit" disabled={busy()}>Sign in</button>
    </form>
  );
}
