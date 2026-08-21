export type PreviewConversation = {
  id: string;
  name: string;
  initials: string;
  preview: string;
  time: string;
  unread: number;
  online?: boolean;
  muted?: boolean;
  color: "green" | "coral" | "gold" | "ink";
};

export type PreviewMessage = {
  id: string;
  author: string;
  initials: string;
  content: string;
  time: string;
  own?: boolean;
  color: PreviewConversation["color"];
};

// TODO(linknest): replace visual fixtures with conversation and history queries.
export const previewConversations: PreviewConversation[] = [
  {
    id: "architecture",
    name: "架构讨论",
    initials: "架",
    preview: "Outbox 的重试边界已经确定",
    time: "10:24",
    unread: 2,
    color: "green",
  },
  {
    id: "lin",
    name: "林舟",
    initials: "林",
    preview: "收到，晚上再看接口文档",
    time: "昨天",
    unread: 0,
    online: true,
    color: "coral",
  },
  {
    id: "weekend",
    name: "周末出游",
    initials: "游",
    preview: "[图片]",
    time: "周三",
    unread: 0,
    muted: true,
    color: "gold",
  },
  {
    id: "product",
    name: "产品备忘",
    initials: "备",
    preview: "已读游标按会话成员维护",
    time: "08/17",
    unread: 0,
    color: "ink",
  },
];

export const previewMessages: PreviewMessage[] = [
  {
    id: "m1",
    author: "林舟",
    initials: "林",
    content: "单聊和群聊统一会话模型后，历史消息查询可以共用一套游标。",
    time: "10:18",
    color: "coral",
  },
  {
    id: "m2",
    author: "我",
    initials: "我",
    content: "对，写入时用 conversation_seq 保证局部有序，客户端重试则看 client_message_id。",
    time: "10:21",
    own: true,
    color: "ink",
  },
  {
    id: "m3",
    author: "陈屿",
    initials: "陈",
    content: "Outbox 的重试边界已经确定，消费端再按 event_id 做幂等。",
    time: "10:24",
    color: "green",
  },
];

