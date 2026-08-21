import { useMemo, useState } from "react";
import {
  BellOff,
  CheckCheck,
  ChevronDown,
  ContactRound,
  FileText,
  Hash,
  Info,
  MessageCircle,
  Mic,
  MoreHorizontal,
  Paperclip,
  PanelLeft,
  Phone,
  Search,
  Send,
  Settings2,
  ShieldCheck,
  Smile,
  SquarePen,
  UsersRound,
  Video,
} from "lucide-react";

import { previewConversations, previewMessages } from "../features/chat/previewData.ts";
import { useChatStore } from "../features/chat/store.ts";
import { Avatar } from "../shared/components/Avatar.tsx";
import { cn } from "../shared/lib/cn.ts";

const navigation = [
  { label: "消息", icon: MessageCircle, active: true },
  { label: "联系人", icon: ContactRound },
  { label: "群组", icon: UsersRound },
];

export function WorkspacePage() {
  const [query, setQuery] = useState("");
  const [draft, setDraft] = useState("");
  const [mobileListOpen, setMobileListOpen] = useState(false);
  const selectedConversationId = useChatStore((state) => state.selectedConversationId);
  const selectConversation = useChatStore((state) => state.selectConversation);
  const selectedConversation =
    previewConversations.find((conversation) => conversation.id === selectedConversationId) ?? previewConversations[0];
  const visibleConversations = useMemo(
    () => previewConversations.filter((conversation) => conversation.name.toLowerCase().includes(query.toLowerCase())),
    [query],
  );

  return (
    <div className={cn("workspace", mobileListOpen && "workspace--mobile-list")}>
      <aside className="app-rail" aria-label="主导航">
        <div className="brand-mark" aria-label="LinkNest IM">LN</div>
        <nav className="rail-navigation">
          {navigation.map(({ label, icon: Icon, active }) => (
            <button className={cn("icon-button", active && "icon-button--active")} type="button" title={label} key={label}>
              <Icon size={20} strokeWidth={1.8} />
            </button>
          ))}
        </nav>
        <div className="rail-bottom">
          <button className="icon-button" type="button" title="设置"><Settings2 size={20} /></button>
          <Avatar initials="我" color="ink" size="small" online />
        </div>
      </aside>

      <section className="conversation-pane" aria-label="会话列表">
        <header className="pane-header">
          <div>
            <p className="eyebrow">LINKNEST</p>
            <h1>消息</h1>
          </div>
          <button className="icon-button icon-button--bordered" type="button" title="新建会话">
            <SquarePen size={18} />
          </button>
        </header>
        <label className="search-field">
          <Search size={16} />
          <input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="搜索会话" />
        </label>
        <div className="filter-row">
          <button className="filter-button filter-button--active" type="button">全部</button>
          <button className="filter-button" type="button">未读</button>
          <button className="filter-button" type="button">群组</button>
        </div>
        <div className="conversation-list">
          {visibleConversations.map((conversation) => (
            <button
              className={cn("conversation-row", selectedConversationId === conversation.id && "conversation-row--active")}
              type="button"
              key={conversation.id}
              onClick={() => {
                selectConversation(conversation.id);
                setMobileListOpen(false);
              }}
            >
              <Avatar initials={conversation.initials} color={conversation.color} online={conversation.online} />
              <span className="conversation-copy">
                <span className="conversation-title-line">
                  <strong>{conversation.name}</strong>
                  <time>{conversation.time}</time>
                </span>
                <span className="conversation-preview-line">
                  <span>{conversation.preview}</span>
                  {conversation.muted ? <BellOff size={13} aria-label="已静音" /> : null}
                  {conversation.unread > 0 ? <b>{conversation.unread}</b> : null}
                </span>
              </span>
            </button>
          ))}
        </div>
      </section>

      <main className="chat-pane">
        <header className="chat-header">
          <div className="chat-identity">
            <button className="icon-button mobile-only" type="button" title="会话列表" onClick={() => setMobileListOpen(true)}>
              <PanelLeft size={19} />
            </button>
            <Avatar initials={selectedConversation.initials} color={selectedConversation.color} />
            <div>
              <h2>{selectedConversation.name}</h2>
              <p><span className="status-dot" /> 3 位成员在线</p>
            </div>
          </div>
          <div className="header-actions">
            <button className="icon-button" type="button" title="语音通话"><Phone size={19} /></button>
            <button className="icon-button" type="button" title="视频通话"><Video size={19} /></button>
            <button className="icon-button" type="button" title="更多"><MoreHorizontal size={20} /></button>
          </div>
        </header>

        <section className="message-history" aria-label="消息记录">
          <div className="date-divider"><span>今天</span></div>
          {previewMessages.map((message) => (
            <article className={cn("message-row", message.own && "message-row--own")} key={message.id}>
              {!message.own ? <Avatar initials={message.initials} color={message.color} size="small" /> : null}
              <div className="message-stack">
                {!message.own ? <span className="message-author">{message.author}</span> : null}
                <div className="message-bubble"><p>{message.content}</p></div>
                <span className="message-meta">{message.time} {message.own ? <CheckCheck size={14} /> : null}</span>
              </div>
            </article>
          ))}
          <div className="typing-row"><Avatar initials="林" color="coral" size="small" /><span><i /><i /><i /></span></div>
        </section>

        <form
          className="composer"
          onSubmit={(event) => {
            event.preventDefault();
            // TODO(linknest): create an optimistic message and send message.send over WebSocket.
          }}
        >
          <div className="composer-tools">
            <button className="icon-button" type="button" title="添加附件"><Paperclip size={19} /></button>
            <button className="icon-button" type="button" title="表情"><Smile size={19} /></button>
          </div>
          <textarea value={draft} onChange={(event) => setDraft(event.target.value)} rows={1} placeholder="输入消息" />
          <div className="composer-tools">
            <button className="icon-button" type="button" title="语音"><Mic size={19} /></button>
            <button className="send-button" type="submit" title="发送" disabled={!draft.trim()}><Send size={18} /></button>
          </div>
        </form>
      </main>

      <aside className="detail-pane" aria-label="会话详情">
        <header className="detail-header">
          <h2>会话详情</h2>
          <button className="icon-button" type="button" title="更多"><MoreHorizontal size={20} /></button>
        </header>
        <section className="group-summary">
          <Avatar initials={selectedConversation.initials} color={selectedConversation.color} size="large" />
          <h3>{selectedConversation.name}</h3>
          <p>4 位成员</p>
          <div className="summary-actions">
            <button type="button"><Search size={17} /><span>搜索</span></button>
            <button type="button"><BellOff size={17} /><span>静音</span></button>
            <button type="button"><Info size={17} /><span>资料</span></button>
          </div>
        </section>
        <section className="detail-section">
          <button className="section-title" type="button"><span>群成员</span><span>4 <ChevronDown size={15} /></span></button>
          <div className="member-list">
            <div><Avatar initials="我" color="ink" size="small" /><span>我</span><ShieldCheck size={14} /></div>
            <div><Avatar initials="林" color="coral" size="small" /><span>林舟</span></div>
            <div><Avatar initials="陈" color="green" size="small" /><span>陈屿</span></div>
            <div><Avatar initials="周" color="gold" size="small" /><span>周宁</span></div>
          </div>
        </section>
        <section className="detail-section">
          <button className="detail-link" type="button"><Hash size={17} /><span>群公告</span><ChevronDown size={15} /></button>
          <button className="detail-link" type="button"><FileText size={17} /><span>共享文件</span><b>12</b></button>
          <button className="detail-link" type="button"><CheckCheck size={17} /><span>已读状态</span><ChevronDown size={15} /></button>
        </section>
      </aside>
    </div>
  );
}
