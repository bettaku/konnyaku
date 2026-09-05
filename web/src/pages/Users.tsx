import { For, createResource } from "solid-js";
import { api, fmtTime } from "../api";
import { useAction } from "../session";
import { formData } from "../ui";

export function UsersPage() {
  const run = useAction();
  const [users, { refetch }] = createResource(() => api.users());
  const create = async (e: SubmitEvent) => {
    const { form, data } = formData(e);
    if (await run(() => api.createUser({ email: data.email, name: data.name, password: data.password, admin: data.admin === "on" }), "User created")) {
      form.reset();
      refetch();
    }
  };
  return (
    <>
      <h1>Users</h1>
      <div class="table-wrap">
        <table>
          <thead><tr><th>ID</th><th>Email</th><th>Name</th><th>Admin</th><th>Created</th></tr></thead>
          <tbody>
            <For each={users()}>
              {(u) => <tr><td>{u.id}</td><td>{u.email}</td><td>{u.name}</td><td>{u.admin ? "yes" : ""}</td><td>{fmtTime(u.created_at)}</td></tr>}
            </For>
          </tbody>
        </table>
      </div>
      <form class="panel row" onSubmit={create}>
        <label>Email<input name="email" type="email" required /></label>
        <label>Name<input name="name" required /></label>
        <label>Password (12–72 bytes)<input name="password" type="password" required minlength="12" autocomplete="new-password" /></label>
        <label class="check"><input name="admin" type="checkbox" /> Administrator</label>
        <button type="submit">Create user</button>
      </form>
    </>
  );
}
