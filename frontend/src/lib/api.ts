import { createAuthApi } from "../api/auth";
import { GoIMApiClient } from "../api/client";
import { createSettingsApi } from "../api/settings";
import { createFriendsApi } from "../api/friends";
import { createGroupsApi } from "../api/groups";
import { createMomentsApi } from "../api/moments";
import { createUploadApi } from "../api/upload";
import { env } from "../config/env";
import { useAuthStore } from "../stores/authStore";

const publicClient = new GoIMApiClient({ baseUrl: env.apiBaseUrl });
export const authApi = createAuthApi(publicClient);

let refreshPromise: Promise<boolean> | null = null;

export function refreshAccessToken(): Promise<boolean> {
  if (refreshPromise) return refreshPromise;

  const pendingRefresh = (async () => {
    const { refreshToken, updateAccessToken, clearSession } = useAuthStore.getState();
    if (!refreshToken) {
      clearSession();
      return false;
    }

    try {
      const response = await authApi.refresh({ refresh_token: refreshToken });
      updateAccessToken(response.access_token, response.expires_in);
      return true;
    } catch {
      clearSession();
      return false;
    }
  })();

  refreshPromise = pendingRefresh;
  void pendingRefresh.finally(() => {
    if (refreshPromise === pendingRefresh) refreshPromise = null;
  });
  return pendingRefresh;
}

export async function ensureFreshSession() {
  const { accessToken, refreshToken, accessTokenExpiresAt } = useAuthStore.getState();
  if (!refreshToken) return false;
  if (accessToken && accessTokenExpiresAt && accessTokenExpiresAt > Date.now() + 30_000) return true;
  return refreshAccessToken();
}

export const apiClient = new GoIMApiClient({
  baseUrl: env.apiBaseUrl,
  getAccessToken: () => useAuthStore.getState().accessToken,
  onUnauthorized: refreshAccessToken,
});

export const accountApi = createAuthApi(apiClient);
export const settingsApi = createSettingsApi(apiClient);
export const friendsApi = createFriendsApi(apiClient);
export const groupsApi = createGroupsApi(apiClient);
export const momentsApi = createMomentsApi(apiClient);
export const uploadApi = createUploadApi(apiClient);
