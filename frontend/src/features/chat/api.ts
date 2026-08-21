import { apiClient } from "../../shared/api/client.ts";
import type { Conversation, CursorPage, Message, MessageType } from "../../shared/types/domain.ts";

export type SendMessageInput = {
  clientMessageId: string;
  type: MessageType;
  body: string;
  replyToMessageId?: string;
};

export const chatApi = {
  conversations: (accessToken: string, cursor = "") =>
    apiClient.get<CursorPage<Conversation>>(`/conversations?cursor=${encodeURIComponent(cursor)}`, accessToken),
  messages: (accessToken: string, conversationId: string, beforeSeq?: number) =>
    apiClient.get<CursorPage<Message>>(
      `/conversations/${conversationId}/messages${beforeSeq ? `?before_seq=${beforeSeq}` : ""}`,
      accessToken,
    ),
  markRead: (accessToken: string, conversationId: string, seq: number) =>
    apiClient.post(`/conversations/${conversationId}/read`, { seq }, accessToken),
};

