import { apiClient } from "../../shared/api/client.ts";
import type { CursorPage, User } from "../../shared/types/domain.ts";

export type Contact = User & { alias: string; isStarred: boolean; isMuted: boolean };

export const contactsApi = {
  list: (accessToken: string, cursor = "") =>
    apiClient.get<CursorPage<Contact>>(`/contacts?cursor=${encodeURIComponent(cursor)}`, accessToken),
  sendRequest: (accessToken: string, addresseeId: string, message: string) =>
    apiClient.post("/friend-requests", { addresseeId, message }, accessToken),
  acceptRequest: (accessToken: string, requestId: string) =>
    apiClient.post(`/friend-requests/${requestId}/accept`, undefined, accessToken),
};

