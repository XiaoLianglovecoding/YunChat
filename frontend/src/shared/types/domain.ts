export type ID = string;

export type User = {
  id: ID;
  username: string;
  nickname: string;
  avatarUrl: string;
  bio: string;
  lastSeenAt: string | null;
};

export type ConversationType = "direct" | "group";

export type Conversation = {
  id: ID;
  type: ConversationType;
  title: string;
  avatarUrl: string;
  unreadCount: number;
  isPinned: boolean;
  isMuted: boolean;
  lastMessage: MessageSummary | null;
};

export type MessageType = "text" | "image" | "file" | "audio" | "video" | "system";

export type MessageSummary = {
  id: ID;
  senderId: ID;
  preview: string;
  sentAt: string;
};

export type Message = {
  id: ID;
  clientMessageId: string;
  conversationId: ID;
  conversationSeq: number;
  senderId: ID;
  type: MessageType;
  body: string;
  sentAt: string;
  status: "sending" | "sent" | "failed" | "recalled";
};

export type CursorPage<T> = {
  items: T[];
  nextCursor: string | null;
};

