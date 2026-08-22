import { apiClient } from "../../shared/api/client.ts";
import type { User, UserSettings } from "../../shared/types/domain.ts";

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
	deviceKey: string;
	deviceName: string;
	platform: "web";
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
	logout: (accessToken: string) => apiClient.post<void>("/auth/logout", undefined, accessToken),
	changePassword: (accessToken: string, currentPassword: string, newPassword: string) =>
		apiClient.post<void>("/auth/change-password", { currentPassword, newPassword }, accessToken),
	getMe: (accessToken: string) => apiClient.get<User>("/users/me", accessToken),
	updateMe: (accessToken: string, input: Partial<Pick<User, "nickname" | "email" | "avatarUrl" | "bio">>) =>
		apiClient.patch<User>("/users/me", input, accessToken),
	getSettings: (accessToken: string) => apiClient.get<UserSettings>("/users/me/settings", accessToken),
	updateSettings: (accessToken: string, input: Partial<Pick<UserSettings, "locale" | "theme" | "notificationEnabled" | "messagePreviewEnabled" | "extra">>) =>
		apiClient.patch<UserSettings>("/users/me/settings", input, accessToken),
};

export function getDeviceIdentity(): Pick<LoginInput, "deviceKey" | "deviceName" | "platform"> {
	const storageKey = "linknest-device-id";
	let deviceKey = localStorage.getItem(storageKey);
	if (!deviceKey) {
		deviceKey = crypto.randomUUID();
		localStorage.setItem(storageKey, deviceKey);
	}
	return { deviceKey, deviceName: `${navigator.platform || "Web"} browser`, platform: "web" };
}
