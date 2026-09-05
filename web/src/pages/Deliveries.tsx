import { For, Show, createResource } from "solid-js";
import { api, fmtTime } from "../api";
import { useAction } from "../session";
import { Badge, Empty } from "../ui";

export function DeliveriesPage() {
  const run = useAction();
  const [rows, { refetch }] = createResource(() => api.deliveries());
  return (
    <>
      <h1>Webhook deliveries</h1>
      <p class="muted small">GitHub push events for configured repositories are queued here and synchronized by the background worker.</p>
      <Show when={rows()?.length} fallback={<Empty>No deliveries recorded.</Empty>}>
        <div class="table-wrap">
          <table>
            <thead><tr><th>Received</th><th>Repository</th><th>Ref</th><th>Status</th><th>Error</th><th /></tr></thead>
            <tbody>
              <For each={rows()}>
                {(d) => (
                  <tr>
                    <td>{fmtTime(d.received_at)}</td>
                    <td>{d.repository_url}</td>
                    <td><code>{d.ref}</code></td>
                    <td><Badge status={d.status} /></td>
                    <td class="small">{d.error}</td>
                    <td>
                      <Show when={d.status === "failed"}>
                        <button class="secondary small" onClick={() => run(() => api.retryDelivery(d.delivery_id), "Queued again").then(() => refetch())}>Retry</button>
                      </Show>
                    </td>
                  </tr>
                )}
              </For>
            </tbody>
          </table>
        </div>
      </Show>
    </>
  );
}
