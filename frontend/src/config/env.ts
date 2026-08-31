const defaultOrigin =
  typeof window === "undefined" || import.meta.env.DEV ? "http://localhost:18080" : window.location.origin;
const defaultApiBaseUrl = `${defaultOrigin}/api/v1`;
const defaultWsUrl = `${defaultOrigin.replace(/^http/, "ws")}/ws`;
const defaultStaticBaseUrl = defaultOrigin;

function trimTrailingSlash(value: string) {
  return value.replace(/\/$/, "");
}

export const env = {
  apiBaseUrl: trimTrailingSlash(import.meta.env.VITE_API_BASE_URL || defaultApiBaseUrl),
  wsUrl: trimTrailingSlash(import.meta.env.VITE_WS_URL || defaultWsUrl),
  staticBaseUrl: trimTrailingSlash(import.meta.env.VITE_STATIC_BASE_URL || defaultStaticBaseUrl),
} as const;
