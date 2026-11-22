import { Show, createEffect, createResource, createSignal, onCleanup, type ParentProps } from "solid-js";
import { A, useLocation } from "@solidjs/router";
import { api } from "./api";
import { SessionProvider, useAction, useSession } from "./session";
import { LoginPage } from "./pages/Login";
import { cycleTheme, theme } from "./theme";
import { ThemeSwitch } from "./ui";

const themeIcon = { auto: "◐", light: "☀", dark: "☾" } as const;
const themeLabel = { auto: "Theme: system", light: "Theme: light", dark: "Theme: dark" } as const;

/** Header theme button for signed-out visitors; signed-in users get the switch inside the user menu. */
function ThemeToggle() {
  return (
    <button class="ghost theme-toggle" onClick={cycleTheme} title={`${themeLabel[theme()]} (click to change)`} aria-label={themeLabel[theme()]}>
      <span aria-hidden="true">{themeIcon[theme()]}</span>
    </button>
  );
}

/** The name in the header opens this menu: account settings, theme, admin pages, and sign-out. */
function UserMenu(props: { onLogout: () => void }) {
  const { user } = useSession();
  const location = useLocation();
  const [open, setOpen] = createSignal(false);
  let root!: HTMLDivElement;
  let trigger!: HTMLButtonElement;

  const onDocClick = (e: MouseEvent) => { if (!root.contains(e.target as Node)) setOpen(false); };
  const onKey = (e: KeyboardEvent) => { if (e.key === "Escape" && open()) { setOpen(false); trigger.focus(); } };
  document.addEventListener("click", onDocClick);
  document.addEventListener("keydown", onKey);
  onCleanup(() => { document.removeEventListener("click", onDocClick); document.removeEventListener("keydown", onKey); });
  // Close after navigating via a menu link.
  createEffect(() => { location.pathname; setOpen(false); });

  const initials = () => user()!.name.trim().split(/\s+/).map((w) => w[0]).slice(0, 2).join("").toUpperCase() || "?";
  return (
    <div class="usermenu" ref={root}>
      <button
        ref={trigger}
        class="usermenu-trigger"
        aria-haspopup="menu"
        aria-expanded={open()}
        onClick={() => setOpen(!open())}
      >
        <span class="avatar" aria-hidden="true">{initials()}</span>
        <span class="usermenu-name">{user()!.name}</span>
        <Show when={user()!.admin}><span class="badge">admin</span></Show>
        <span class="caret" aria-hidden="true">▾</span>
      </button>
      <Show when={open()}>
        <div class="usermenu-pop" role="menu">
          <div class="usermenu-head">
            <strong>{user()!.name}</strong>
            <span class="muted small">{user()!.email}</span>
          </div>
          <A href="/account" role="menuitem">Account settings</A>
          <Show when={!user()!.admin}><A href="/locales" role="menuitem">Locales</A></Show>
          <div class="usermenu-theme">
            <span class="muted small">Theme</span>
            <ThemeSwitch compact />
          </div>
          <Show when={user()!.admin}>
            <div class="usermenu-section">Administration</div>
            <A href="/users" role="menuitem">Users</A>
            <A href="/locales" role="menuitem">Locales</A>
            <A href="/deliveries" role="menuitem">Webhook deliveries</A>
          </Show>
          <div class="usermenu-sep" />
          <button class="usermenu-item danger-text" role="menuitem" onClick={() => { setOpen(false); props.onLogout(); }}>Log out</button>
        </div>
      </Show>
    </div>
  );
}

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
          </nav>
          <UserMenu onLogout={logout} />
        </Show>
        <Show when={!user()}><ThemeToggle /></Show>
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
