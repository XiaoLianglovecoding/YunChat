import { create } from "zustand";
import { createJSONStorage, persist } from "zustand/middleware";
import type { LoginResponse } from "../../goim-api-types";
import { env } from "../config/env";

export interface AuthUser {
  id: number;
  username: string;
  avatarUrl?: string;
}

interface AuthState {
  accessToken: string | null;
  refreshToken: string | null;
  accessTokenExpiresAt: number | null;
  user: AuthUser | null;
  previewMode: boolean;
  setSession: (response: LoginResponse, fallbackUsername?: string) => void;
  enterPreview: () => void;
  updateAccessToken: (accessToken: string, expiresIn: number) => void;
  clearSession: () => void;
  setAvatarUrl: (avatarUrl: string) => void;
}

interface AccessTokenClaims {
  user_id?: number;
  username?: string;
  exp?: number;
}

export function decodeAccessToken(token: string): AccessTokenClaims | null {
  try {
    const payload = token.split(".")[1];
    if (!payload) return null;
    const normalized = payload.replace(/-/g, "+").replace(/_/g, "/");
    const padded = normalized.padEnd(Math.ceil(normalized.length / 4) * 4, "=");
    const bytes = Uint8Array.from(atob(padded), (character) => character.charCodeAt(0));
    return JSON.parse(new TextDecoder().decode(bytes)) as AccessTokenClaims;
  } catch {
    return null;
  }
}

export const useAuthStore = create<AuthState>()(
  persist(
    (set) => ({
      accessToken: null,
      refreshToken: null,
      accessTokenExpiresAt: null,
      user: null,
      previewMode: false,
      setSession: (response, fallbackUsername) => {
        const claims = decodeAccessToken(response.access_token);
        set({
          accessToken: response.access_token,
          refreshToken: response.refresh_token,
          accessTokenExpiresAt: claims?.exp ? claims.exp * 1000 : Date.now() + response.expires_in * 1000,
          user: {
            id: claims?.user_id ?? 0,
            username: claims?.username ?? fallbackUsername ?? "用户",
            avatarUrl: response.avatar_url ? (response.avatar_url.startsWith("http") ? response.avatar_url : `${env.staticBaseUrl}${response.avatar_url}`) : undefined,
          },
          previewMode: false,
        });
      },
      enterPreview: () => set({
        accessToken: null,
        refreshToken: null,
        accessTokenExpiresAt: null,
        user: { id: 10086, username: "顾言" },
        previewMode: true,
      }),
      updateAccessToken: (accessToken, expiresIn) => {
        const claims = decodeAccessToken(accessToken);
        set((state) => ({
          accessToken,
          accessTokenExpiresAt: claims?.exp ? claims.exp * 1000 : Date.now() + expiresIn * 1000,
          user: claims?.user_id
            ? { id: claims.user_id, username: claims.username ?? state.user?.username ?? "用户", avatarUrl: state.user?.avatarUrl }
            : state.user,
        }));
      },
      clearSession: () => set({ accessToken: null, refreshToken: null, accessTokenExpiresAt: null, user: null, previewMode: false }),
      setAvatarUrl: (avatarUrl) => set((state) => ({ user: state.user ? { ...state.user, avatarUrl } : null })),
    }),
    {
      name: "goim-auth-v1",
      storage: createJSONStorage(() => localStorage),
      partialize: ({ accessToken, refreshToken, accessTokenExpiresAt, user, previewMode }) => ({ accessToken, refreshToken, accessTokenExpiresAt, user, previewMode }),
    },
  ),
);
