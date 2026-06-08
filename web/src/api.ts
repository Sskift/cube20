// Cloud token storage + the thin fetch wrapper every dashboard call goes through.
// The token is read once from the ?token= query param (then scrubbed from the
// URL) and persisted to localStorage; apiJSON attaches it as a Bearer header.

const CLOUD_TOKEN_KEY = "cube20.cloudToken";
let cloudTokenSynced = false;

export function cloudToken() {
  if (typeof window === "undefined") return "";
  if (!cloudTokenSynced) {
    cloudTokenSynced = true;
    const params = new URLSearchParams(window.location.search);
    const token = params.get("token");
    if (token) {
      window.localStorage.setItem(CLOUD_TOKEN_KEY, token);
      params.delete("token");
      const nextQuery = params.toString();
      const nextURL = `${window.location.pathname}${nextQuery ? `?${nextQuery}` : ""}${window.location.hash}`;
      window.history.replaceState(null, "", nextURL);
    }
  }
  return window.localStorage.getItem(CLOUD_TOKEN_KEY) || "";
}

export function saveCloudToken(token: string) {
  if (typeof window === "undefined") return;
  cloudTokenSynced = true;
  const trimmed = token.trim();
  if (trimmed) {
    window.localStorage.setItem(CLOUD_TOKEN_KEY, trimmed);
  } else {
    window.localStorage.removeItem(CLOUD_TOKEN_KEY);
  }
}

export function cloudOrigin() {
  if (typeof window === "undefined") return "";
  return window.location.origin;
}

export async function apiJSON<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers);
  if (!headers.has("Content-Type")) headers.set("Content-Type", "application/json");
  const token = cloudToken();
  if (token && !headers.has("Authorization")) headers.set("Authorization", `Bearer ${token}`);
  const response = await fetch(path, { ...init, headers });
  const text = await response.text();
  const data = text ? JSON.parse(text) : {};
  if (!response.ok) throw new Error(data.error || response.statusText);
  return data as T;
}
