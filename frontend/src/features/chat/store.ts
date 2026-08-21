import { create } from "zustand";

import type { Message } from "../../shared/types/domain.ts";

type ChatState = {
  selectedConversationId: string | null;
  messagesByConversation: Record<string, Message[]>;
  selectConversation: (conversationId: string) => void;
  replaceMessages: (conversationId: string, messages: Message[]) => void;
  appendMessage: (message: Message) => void;
};

export const useChatStore = create<ChatState>((set) => ({
  selectedConversationId: "architecture",
  messagesByConversation: {},
  selectConversation: (selectedConversationId) => set({ selectedConversationId }),
  replaceMessages: (conversationId, messages) =>
    set((state) => ({ messagesByConversation: { ...state.messagesByConversation, [conversationId]: messages } })),
  appendMessage: (message) =>
    set((state) => ({
      messagesByConversation: {
        ...state.messagesByConversation,
        [message.conversationId]: [...(state.messagesByConversation[message.conversationId] ?? []), message],
      },
    })),
}));

