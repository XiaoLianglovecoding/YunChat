import { create } from "zustand";
import { persist } from "zustand/middleware";

import type { User } from "../../shared/types/domain.ts";

type AuthState = {
  user: User | null;
  accessToken: string | null;
  refreshToken: string | null;
  setSession: (user: User, accessToken: string, refreshToken: string) => void;
  clearSession: () => void;
};

export const useAuthStore = create<AuthState>()(
  persist(
    (set) => ({
      user: null,
      accessToken: null,
      refreshToken: null,
      setSession: (user, accessToken, refreshToken) => set({ user, accessToken, refreshToken }),
      clearSession: () => set({ user: null, accessToken: null, refreshToken: null }),
    }),
    {
      name: "linknest-auth",
      partialize: ({ user, refreshToken }) => ({ user, refreshToken }),
    },
  ),
);

