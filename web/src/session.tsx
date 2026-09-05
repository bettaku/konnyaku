import { createContext, createResource, createSignal, useContext, type ParentProps } from "solid-js";
import { api, setUnauthorizedHandler, type Locale, type User } from "./api";

type Session = {
  user: () => User | null;
  setUser: (u: User | null) => void;
  locales: () => Locale[];
  refreshLocales: () => void;
  notify: (message: string, ok?: boolean) => void;
  flash: () => { message: string; ok: boolean } | null;
};
const Ctx = createContext<Session>();

export function SessionProvider(props: ParentProps) {
  const [user, setUser] = createSignal<User | null>(null);
  const [flash, setFlash] = createSignal<{ message: string; ok: boolean } | null>(null);
  const [localeList, { refetch }] = createResource(() => (user() ? true : null), () => api.locales());
  const locales = () => localeList() ?? [];
  let timer: ReturnType<typeof setTimeout> | undefined;
  const notify = (message: string, ok = false) => {
    setFlash({ message, ok });
    clearTimeout(timer);
    timer = setTimeout(() => setFlash(null), ok ? 3500 : 9000);
  };
  setUnauthorizedHandler(() => setUser(null));
  const value: Session = { user, setUser, locales, refreshLocales: () => void refetch(), notify, flash };
  return <Ctx.Provider value={value}>{props.children}</Ctx.Provider>;
}
export const useSession = () => {
  const s = useContext(Ctx);
  if (!s) throw new Error("session context missing");
  return s;
};

/** Wraps an async action: surfaces errors as flash messages and returns whether it succeeded. */
export const useAction = () => {
  const { notify } = useSession();
  return async (fn: () => Promise<unknown>, success?: string): Promise<boolean> => {
    try {
      await fn();
      if (success) notify(success, true);
      return true;
    } catch (err) {
      notify(err instanceof Error ? err.message : String(err));
      return false;
    }
  };
};
