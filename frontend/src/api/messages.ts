import type { ApiId, Page, PrivateMessage, RevokeMessageRequest, SearchMessagesQuery } from "../../goim-api-types";
import type { GoIMApiClient } from "./client";

export const createMessagesApi = (client: GoIMApiClient) => ({
  revoke: (input: RevokeMessageRequest) => client.post<void>("/msg/revoke", input),
  remove: (messageId: ApiId, convId: string) => client.delete<void>(`/msg/${messageId}?convId=${encodeURIComponent(convId)}`),
  search: ({ q, limit = 20, offset = 0 }: SearchMessagesQuery) => client.get<Page<PrivateMessage>>(`/msg/search?q=${encodeURIComponent(q)}&limit=${limit}&offset=${offset}`),
});
