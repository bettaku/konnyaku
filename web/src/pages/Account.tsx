import { Show, createSignal } from "solid-js";
import { api } from "../api";
import { useAction, useSession } from "../session";
import { Crumbs, ThemeSwitch, formData } from "../ui";

export function AccountPage() {
  const { user, setUser, notify } = useSession();
  const run = useAction();
  const [pwError, setPwError] = createSignal("");

  const saveProfile = async (e: SubmitEvent) => {
    const { data } = formData(e);
    await run(async () => setUser(await api.updateMe(data.name)), "Profile saved");
  };

  const changePassword = async (e: SubmitEvent) => {
    const { form, data } = formData(e);
    setPwError("");
    if (data.new_password !== data.confirm) {
      setPwError("The new passwords do not match.");
      return;
    }
    try {
      const r = await api.changePassword(data.current_password, data.new_password);
      form.reset();
      notify(`Password changed${r.signed_out ? `; signed out ${r.signed_out} other session${r.signed_out === 1 ? "" : "s"}` : ""}`, true);
    } catch (err) {
      setPwError(err instanceof Error ? err.message : String(err));
    }
  };

  const signOutOthers = async () => {
    if (!confirm("Sign out of every other browser and device where you are signed in?")) return;
    try {
      const r = await api.signOutOthers();
      notify(r.signed_out ? `Signed out ${r.signed_out} other session${r.signed_out === 1 ? "" : "s"}` : "No other sessions were active", true);
    } catch (err) {
      notify(err instanceof Error ? err.message : String(err));
    }
  };

  return (
    <Show when={user()}>
      {(u) => (
        <div class="narrow-wide">
          <Crumbs items={[{ label: "Account settings" }]} />
          <h1>Account settings</h1>

          <section class="settings-section">
            <div class="settings-intro">
              <h2>Profile</h2>
              <p class="muted small">Your name appears in translation history and member lists.</p>
            </div>
            <form class="panel settings-form" onSubmit={saveProfile}>
              <label>Display name<input name="name" value={u().name} required maxLength={200} autocomplete="name" /></label>
              <label>Email<input value={u().email} disabled /></label>
              <p class="muted small m0">Your email is your sign-in ID and can't be changed here.</p>
              <div><button type="submit">Save profile</button></div>
            </form>
          </section>

          <section class="settings-section">
            <div class="settings-intro">
              <h2>Password</h2>
              <p class="muted small">Use 12–72 characters. Changing it signs you out everywhere else.</p>
            </div>
            <form class="panel settings-form" onSubmit={changePassword}>
              {/* Hidden username lets password managers attach the new password to the right account. */}
              <input type="email" name="username" value={u().email} autocomplete="username" hidden readOnly />
              <label>Current password<input name="current_password" type="password" required autocomplete="current-password" /></label>
              <label>New password<input name="new_password" type="password" required minLength={12} maxLength={72} autocomplete="new-password" /></label>
              <label>Confirm new password<input name="confirm" type="password" required minLength={12} maxLength={72} autocomplete="new-password" /></label>
              <Show when={pwError()}><div class="error" role="alert">{pwError()}</div></Show>
              <div><button type="submit">Change password</button></div>
            </form>
          </section>

          <section class="settings-section">
            <div class="settings-intro">
              <h2>Appearance</h2>
              <p class="muted small">Saved in this browser.</p>
            </div>
            <div class="panel settings-form">
              <ThemeSwitch />
            </div>
          </section>

          <section class="settings-section">
            <div class="settings-intro">
              <h2>Sessions</h2>
              <p class="muted small">Signed in on a shared or lost device?</p>
            </div>
            <div class="panel settings-form">
              <p class="m0">Sign out of every other browser and device. This one stays signed in.</p>
              <div><button type="button" class="danger" onClick={signOutOthers}>Sign out other sessions</button></div>
            </div>
          </section>
        </div>
      )}
    </Show>
  );
}
