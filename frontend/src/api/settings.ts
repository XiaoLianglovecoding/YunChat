import type { MuteConversationRequest, UpdateSettingsRequest, UserSettings } from "../../goim-api-types";
import type { GoIMApiClient } from "./client";

export const createSettingsApi = (client: GoIMApiClient) => ({
  get: () => client.get<UserSettings>("/settings"),
  update: (input: UpdateSettingsRequest) => client.put<void>("/settings", input),
  mute: (input: MuteConversationRequest) => client.post<void>("/settings/mute", input),
  unmute: (convId: string) => client.delete<void>(`/settings/mute/${encodeURIComponent(convId)}`),
});
