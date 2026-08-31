export interface MockConversation {
  id: string;
  name: string;
  preview: string;
  time: string;
  unread: number;
  online?: boolean;
  muted?: boolean;
  group?: boolean;
}

export interface MockMessage {
  id: number;
  from: "me" | "other";
  content: string;
  time: string;
  status?: "sent" | "pending" | "failed";
}

export interface MockContact {
  id: string;
  name: string;
  handle: string;
  online?: boolean;
  location: string;
  groups: number;
  note: string;
}

export const conversations: MockConversation[] = [
  { id: "lin-cheng", name: "林澄", preview: "项目的方向很清晰，明天继续。", time: "10:24", unread: 2, online: true },
  { id: "product-team", name: "产品讨论组", preview: "周一一起看第一版交互", time: "09:48", unread: 0, group: true },
  { id: "zhou-yu", name: "周屿", preview: "好的，晚点见", time: "昨天", unread: 0, online: false },
  { id: "design-room", name: "设计协作", preview: "新的视觉稿已同步", time: "周五", unread: 5, muted: true, group: true },
  { id: "chen-xi", name: "陈曦", preview: "收到，谢谢", time: "周四", unread: 0, online: true },
  { id: "lu-yao", name: "陆遥", preview: "还没有消息，打个招呼吧", time: "", unread: 0, online: false },
];

export const messagesByConversation: Record<string, MockMessage[]> = {
  "lin-cheng": [
    { id: 1, from: "other", content: "早上好，我看过昨天整理的前端效果说明了。", time: "10:16" },
    { id: 2, from: "me", content: "感觉怎么样？我想把整体做得更轻一些，信息密度不要太高。", time: "10:18", status: "sent" },
    { id: 3, from: "other", content: "很合适。三栏结构清楚，但视觉比传统客户端更舒展。", time: "10:21" },
    { id: 4, from: "other", content: "项目的方向很清晰，明天继续。", time: "10:24" },
  ],
  "product-team": [
    { id: 1, from: "other", content: "大家好，第一版先把文字聊天闭环跑通。", time: "09:35" },
    { id: 2, from: "me", content: "没问题，媒体能力放到后续前后端联合迭代。", time: "09:40", status: "sent" },
    { id: 3, from: "other", content: "周一一起看第一版交互。", time: "09:48" },
  ],
  "zhou-yu": [{ id: 1, from: "other", content: "好的，晚点见。", time: "昨天" }],
  "chen-xi": [{ id: 1, from: "other", content: "收到，谢谢。", time: "周四" }],
  "lu-yao": [],
};

export const contacts: MockContact[] = [
  { id: "chen-xi", name: "陈曦", handle: "chenxi", online: true, location: "上海", groups: 2, note: "产品设计师" },
  { id: "lin-cheng", name: "林澄", handle: "lincheng", online: true, location: "杭州", groups: 3, note: "保持好奇，持续创造。" },
  { id: "lu-yao", name: "陆遥", handle: "luyao", online: false, location: "北京", groups: 1, note: "后端工程师" },
  { id: "zhou-yu", name: "周屿", handle: "zhouyu", online: false, location: "深圳", groups: 2, note: "最近在学习摄影。" },
];

export const momentPosts = [
  { id: 1, author: "林澄", time: "12 分钟前", content: "把复杂的事情讲清楚，本身就是一种设计。今天终于把新项目的交互主线梳理完了。", likes: ["陈曦", "周屿"], comments: [{ author: "陈曦", content: "期待第一版。" }] },
  { id: 2, author: "周屿", time: "2 小时前", content: "午后的光落在桌面上，适合安静地把手头的事情做完。", likes: ["林澄"], comments: [] },
  { id: 3, author: "陈曦", time: "昨天 21:36", content: "比起堆叠功能，我更喜欢让每一个必要的功能都自然地出现在它该出现的位置。", likes: [], comments: [{ author: "林澄", content: "深有同感。" }] },
];
