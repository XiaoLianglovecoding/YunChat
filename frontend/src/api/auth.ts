import type { LoginResponse, RefreshRequest, RegisterRequest, RegisterResponse, UpdatePasswordRequest, UpdateUsernameRequest } from "../../goim-api-types";
import type { GoIMApiClient } from "./client";

export const createAuthApi = (client: GoIMApiClient) => ({
  register: (input: RegisterRequest) => client.post<RegisterResponse>("/auth/register", input),
  login: (input: RegisterRequest) => client.post<LoginResponse>("/auth/login", input),
  refresh: (input: RefreshRequest) => client.post<Pick<LoginResponse, "access_token" | "expires_in">>("/auth/refresh", input),
  updateUsername: (input: UpdateUsernameRequest) => client.put<LoginResponse>("/account/username", input),
  updatePassword: (input: UpdatePasswordRequest) => client.put<void>("/account/password", input),
});
