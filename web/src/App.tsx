import { Show, createResource, type ParentProps } from "solid-js";
import { A } from "@solidjs/router";
import { api } from "./api";
import { SessionProvider, useAction, useSession } from "./session";
import { LoginPage } from "./pages/Login";

function Shell(props: ParentProps) {
  const { user, setUser, flash } = useSession();
  const run = useAction();
  const [booted] = createResource(async () => {
    try {
      setUser(await api.me());
    } catch {
      setUser(null);
    }
    return true;
  });
  const logout = () => run(async () => {
    await api.logout();
    setUser(null);
  });
  return (
    <>
      <header class="topbar">
        <A href="/projects" class="brand">Konnyaku</A>
        <Show when={user()}>
          <nav>
            <A href="/projects">Projects</A>
            <A href="/locales">Locales</A>
            <Show when={user()!.admin}>
              <A href="/users">Users</A>
              <A href="/deliveries">Webhooks</A>
            </Show>
          </nav>
          <span class="who">{user()!.name}{user()!.admin ? " (admin)" : ""}</span>
          <button class="secondary small" onClick={logout}>Log out</button>
        </Show>
      </header>
      <main class="page">
        <Show when={booted()}>
          <Show when={user()} fallback={<LoginPage />}>{props.children}</Show>
        </Show>
      </main>
      <Show when={flash()}>{(f) => <div class={"flash" + (f().ok ? " ok" : "")} role="status">{f().message}</div>}</Show>
    </>
  );
}
export function App(props: ParentProps) {
  return (
    <SessionProvider>
      <Shell>{props.children}</Shell>
    </SessionProvider>
  );
}
