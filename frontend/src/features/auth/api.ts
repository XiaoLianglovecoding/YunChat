import { apiClient } from "../../shared/api/client.ts";
import type { User } from "../../shared/types/domain.ts";

export type LoginInput = {
  login: string;
  password: string;
  deviceKey: string;
  deviceName: string;
  platform: "web";
};

export type RegisterInput = {
  username: string;
  nickname: string;
  email?: string;
  password: string;
};

export type AuthSession = {
  user: User;
  accessToken: string;
  refreshToken: string;
  expiresIn: number;
};

export const authApi = {
  login: (input: LoginInput) => apiClient.post<AuthSession>("/auth/login", input),
  register: (input: RegisterInput) => apiClient.post<AuthSession>("/auth/register", input),
  refresh: (refreshToken: string) => apiClient.post<AuthSession>("/auth/refresh", { refreshToken }),
};

