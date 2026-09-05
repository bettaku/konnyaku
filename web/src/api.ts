export class ApiError extends Error {
  constructor(public status: number, message: string) {
    super(message);
  }
}

export type User = { id: number; email: string; name: string; admin: boolean; created_at?: string };
export type Locale = { code: string; name: string };
export type Project = { id: number; slug: string; name: string; source_locale: string; created_at: string };
export type ProjectDetail = { project: Project; role: Role; locales: Locale[] };
export type ComponentDetail = { component: Component; project: Project; role: Role; locales: Locale[] };
export type Role = "viewer" | "translator" | "manager" | "admin";
export type Component = {
  id: number;
  project_id: number;
  slug: string;
  name: string;
  format: Format;
  file_pattern: string;
  repository_id: number | null;
};
export type Format = "json" | "yaml" | "po" | "android";
export type Repository = { id: number; project_id: number; name: string; url: string; branch: string; created_at: string };
export type Checkout = { exists: boolean; branch: string; commit: string; subject: string; dirty: boolean; modified: number };
export type RepositoryStatus = { repository: Repository; checkout: Checkout; components: Component[]; github_token: boolean };
export type Candidate = { pattern: string; format: Format; locales: string[] };
export type SyncFile = { component: string; locale: string; path: string; imported: number; unknown: number; empty: number };
export type SyncResult = { files: SyncFile[]; ignored: string[]; errors: string[] };
export type Member = { user_id: number; email: string; name: string; role: Role };
export type Status = "untranslated" | "translated" | "reviewed" | "needs_review";
export type Unit = { id: number; key: string; source: string; value: string; status: Status; version: number; updated_at: string | null };
export type UnitPage = { total: number; offset: number; limit: number; units: Unit[] };
export type Translation = { unit_id: number; locale: string; value: string; status: Status; version: number; updated_at: string };
export type Stat = { locale: string; total: number; translated: number; reviewed: number; needs_review: number };
export type ProjectStat = Stat & { component_id: number };
export type HistoryEntry = {
  id: number;
  value: string;
  status: Status;
  version: number;
  changed_at: string;
  changed_by: number | null;
  changed_by_name: string;
};
export type ActivityEntry = HistoryEntry & { unit_id: number; key: string; locale: string; component_id?: number; component_name?: string };
export type MemoryMatch = { source: string; value: string; status: Status; component_name: string; project_name: string; score: number };
export type GlossaryTerm = { id: number; project_id: number; locale: string; term: string; translation: string; note: string; updated_by: number | null; updated_at: string; updated_by_name?: string };
export type Assist = { memory: MemoryMatch[]; glossary: GlossaryTerm[] };
export type ImportIssue = { component_id: number; locale: string; key: string; value: string; seen_at: string };
export type Delivery = { delivery_id: string; received_at: string; repository_url: string; ref: string; status: string; error: string };

let onUnauthorized: (() => void) | null = null;
export const setUnauthorizedHandler = (fn: () => void) => (onUnauthorized = fn);

async function request<T>(method: string, path: string, body?: unknown, raw = false): Promise<T> {
  const headers: Record<string, string> = { "X-Requested-With": "konnyaku" };
  let payload: BodyInit | undefined;
  if (body !== undefined) {
    if (raw) payload = body as BodyInit;
    else {
      headers["Content-Type"] = "application/json";
      payload = JSON.stringify(body);
    }
  }
  const res = await fetch("/api" + path, { method, headers, body: payload, credentials: "same-origin" });
  if (res.status === 204) return undefined as T;
  const text = await res.text();
  let data: unknown = null;
  try {
    data = text ? JSON.parse(text) : null;
  } catch {
    data = { error: text };
  }
  if (!res.ok) {
    if (res.status === 401 && path !== "/login" && path !== "/me") onUnauthorized?.();
    const message = (data as { error?: string } | null)?.error || res.statusText;
    throw new ApiError(res.status, message);
  }
  return data as T;
}

const enc = encodeURIComponent;
export const api = {
  me: () => request<User>("GET", "/me"),
  login: (email: string, password: string) => request<User>("POST", "/login", { email, password }),
  logout: () => request<void>("POST", "/logout"),
  users: () => request<User[]>("GET", "/users"),
  createUser: (u: { email: string; name: string; password: string; admin: boolean }) => request<User>("POST", "/users", u),
  locales: () => request<Locale[]>("GET", "/locales"),
  saveLocale: (code: string, name: string) => request<Locale>("POST", "/locales", { code, name }),
  deleteLocale: (code: string) => request<void>("DELETE", `/locales/${enc(code)}`),
  projects: () => request<Project[]>("GET", "/projects"),
  createProject: (p: { slug: string; name: string; source_locale: string }) => request<Project>("POST", "/projects", p),
  project: (id: number) => request<ProjectDetail>("GET", `/projects/${id}`),
  renameProject: (id: number, name: string) => request<Project>("PATCH", `/projects/${id}`, { name }),
  deleteProject: (id: number) => request<void>("DELETE", `/projects/${id}`),
  projectStats: (id: number) => request<ProjectStat[]>("GET", `/projects/${id}/stats`),
  projectHistory: (id: number) => request<ActivityEntry[]>("GET", `/projects/${id}/history`),
  addProjectLocale: (id: number, locale: string) => request<void>("PUT", `/projects/${id}/locales/${enc(locale)}`),
  removeProjectLocale: (id: number, locale: string) => request<void>("DELETE", `/projects/${id}/locales/${enc(locale)}`),
  members: (id: number) => request<Member[]>("GET", `/projects/${id}/members`),
  saveMember: (id: number, user: number, role: string) => request<void>("PUT", `/projects/${id}/members/${user}`, { role }),
  deleteMember: (id: number, user: number) => request<void>("DELETE", `/projects/${id}/members/${user}`),
  components: (id: number) => request<Component[]>("GET", `/projects/${id}/components`),
  createComponent: (id: number, c: { slug: string; name: string; format: Format; repository_id?: number | null; file_pattern?: string }) =>
    request<Component>("POST", `/projects/${id}/components`, c),
  component: (id: number) => request<ComponentDetail>("GET", `/components/${id}`),
  updateComponent: (id: number, c: { name?: string; repository_id?: number | null; file_pattern?: string }) =>
    request<Component>("PATCH", `/components/${id}`, c),
  deleteComponent: (id: number) => request<void>("DELETE", `/components/${id}`),
  componentStats: (id: number) => request<Stat[]>("GET", `/components/${id}/stats`),
  componentIssues: (id: number) => request<ImportIssue[]>("GET", `/components/${id}/issues`),
  dismissIssue: (id: number, locale: string, key: string) => request<{ dismissed: number }>("POST", `/components/${id}/issues/dismiss`, { locale, key }),
  projectIssues: (id: number) => request<Array<{ component_id: number; issues: number }>>("GET", `/projects/${id}/issues`),
  componentHistory: (id: number, locale?: string) =>
    request<ActivityEntry[]>("GET", `/components/${id}/history${locale ? `?locale=${enc(locale)}` : ""}`),
  units: (id: number, p: { locale: string; offset?: number; q?: string; status?: string }) => {
    const qs = new URLSearchParams({ locale: p.locale });
    if (p.offset) qs.set("offset", String(p.offset));
    if (p.q) qs.set("q", p.q);
    if (p.status) qs.set("status", p.status);
    return request<UnitPage>("GET", `/components/${id}/units?${qs}`);
  },
  importFile: (id: number, locale: string, file: File) =>
    request<{ imported: number; unknown: number; empty: number }>("POST", `/components/${id}/import?locale=${enc(locale)}`, file, true),
  exportUrl: (id: number, locale: string) => `/api/components/${id}/export?locale=${enc(locale)}`,
  assist: (id: number, locale: string) => request<Assist>("GET", `/units/${id}/assist?locale=${enc(locale)}`),
  glossary: (project: number, locale?: string) => request<GlossaryTerm[]>("GET", `/projects/${project}/glossary${locale ? `?locale=${enc(locale)}` : ""}`),
  saveGlossaryTerm: (project: number, t: { locale: string; term: string; translation: string; note: string }) =>
    request<GlossaryTerm>("POST", `/projects/${project}/glossary`, t),
  glossaryExportUrl: (project: number, locale?: string) => `/api/projects/${project}/glossary/export${locale ? `?locale=${enc(locale)}` : ""}`,
  importGlossary: (project: number, file: File, locale?: string) =>
    request<{ imported: number; skipped: number }>("POST", `/projects/${project}/glossary/import${locale ? `?locale=${enc(locale)}` : ""}`, file, true),
  autofill: (component: number, locale: string, dryRun: boolean, status: "needs_review" | "translated" = "needs_review") =>
    request<{ untranslated: number; matches: number; filled: number }>("POST", `/components/${component}/autofill`, { locale, status, dry_run: dryRun }),
  deleteGlossaryTerm: (project: number, id: number) => request<void>("DELETE", `/projects/${project}/glossary/${id}`),
  unitHistory: (id: number, locale: string) => request<HistoryEntry[]>("GET", `/units/${id}/history?locale=${enc(locale)}`),
  saveTranslation: (id: number, locale: string, t: { value: string; status: Status; version: number }) =>
    request<Translation>("PUT", `/units/${id}/translations/${enc(locale)}`, t),
  suggest: (id: number, provider: "openai" | "google", locale: string) =>
    request<{ value: string; provider: string }>("POST", `/units/${id}/suggest`, { provider, locale }),
  repositories: (id: number) => request<Repository[]>("GET", `/projects/${id}/repositories`),
  createRepository: (id: number, r: { name?: string; url: string; branch?: string }) =>
    request<Repository>("POST", `/projects/${id}/repositories`, r),
  repository: (id: number) => request<RepositoryStatus>("GET", `/repositories/${id}`),
  deleteRepository: (id: number) => request<void>("DELETE", `/repositories/${id}`),
  scanRepository: (id: number) => request<Candidate[]>("GET", `/repositories/${id}/scan`),
  repositoryAction: (id: number, action: "clone" | "pull" | "push" | "sync" | "commit", message = "") =>
    request<{ status: string; sync?: SyncResult; committed?: boolean }>("POST", `/repositories/${id}/git/${action}`, { message }),
  pullRequest: (id: number, title: string, body: string) =>
    request<{ url: string; branch: string }>("POST", `/repositories/${id}/pull-request`, { title, body }),
  deliveries: () => request<Delivery[]>("GET", "/deliveries"),
  retryDelivery: (id: string) => request<void>("POST", `/deliveries/${enc(id)}/retry`),
};

export const percent = (s: { total: number; translated: number }) => (s.total ? Math.round((s.translated / s.total) * 1000) / 10 : 0);
export const fmtTime = (t: string | null | undefined) => (t ? new Date(t).toLocaleString() : "");
