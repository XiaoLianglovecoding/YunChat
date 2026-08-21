import { apiClient } from "../../shared/api/client.ts";
import type { Conversation } from "../../shared/types/domain.ts";

export type CreateGroupInput = { title: string; memberIds: string[] };

export const groupsApi = {
  create: (accessToken: string, input: CreateGroupInput) =>
    apiClient.post<Conversation>("/groups", input, accessToken),
  addMembers: (accessToken: string, groupId: string, memberIds: string[]) =>
    apiClient.post(`/groups/${groupId}/members`, { memberIds }, accessToken),
};

