import { createSignal } from "solid-js";

/** "auto" follows the OS setting; "light"/"dark" pin the theme via <html data-theme>. */
export type ThemeChoice = "auto" | "light" | "dark";

const KEY = "konnyaku-theme";
const order: ThemeChoice[] = ["auto", "light", "dark"];

const load = (): ThemeChoice => {
  try {
    const v = localStorage.getItem(KEY);
    return v === "light" || v === "dark" ? v : "auto";
  } catch {
    return "auto";
  }
};
const apply = (t: ThemeChoice) => {
  if (t === "auto") document.documentElement.removeAttribute("data-theme");
  else document.documentElement.setAttribute("data-theme", t);
};

const [theme, setThemeSignal] = createSignal<ThemeChoice>(load());
// Applied at import time (main.tsx imports this first) so the page never flashes the wrong theme.
apply(theme());

export { theme };
export const setTheme = (t: ThemeChoice) => {
  setThemeSignal(t);
  apply(t);
  try {
    if (t === "auto") localStorage.removeItem(KEY);
    else localStorage.setItem(KEY, t);
  } catch {
    /* storage unavailable: the choice lasts for this page load only */
  }
};
export const cycleTheme = () => setTheme(order[(order.indexOf(theme()) + 1) % order.length]);
