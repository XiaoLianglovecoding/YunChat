import { motion, useReducedMotion } from "framer-motion";
import { Contact, MessageCircle, Settings, Sparkles, UsersRound } from "lucide-react";
import { NavLink, Outlet, useLocation } from "react-router-dom";
import { Avatar } from "../ui";
import { useAuthStore } from "../../stores/authStore";
import { RealtimeBootstrap } from "../realtime/RealtimeBootstrap";
import { NetworkStatus } from "../system/NetworkStatus";

const navItems = [
  { to: "/app/chats", label: "聊天", icon: MessageCircle, match: "/app/chats" },
  { to: "/app/contacts", label: "联系人", icon: Contact, match: "/app/contacts" },
  { to: "/app/moments", label: "朋友圈", icon: UsersRound, match: "/app/moments" },
  { to: "/app/settings/profile", label: "设置", icon: Settings, match: "/app/settings" },
];

export function AppShell() {
  const location = useLocation();
  const reduceMotion = useReducedMotion();
  const user = useAuthStore((state) => state.user);

  return (
    <div className="app-viewport">
      <RealtimeBootstrap />
      <NetworkStatus />
      <div className="desktop-size-notice">
        <Sparkles size={22} />
        <h1>需要更宽的窗口</h1>
        <p>GoIM 首期为桌面 Web 设计，请将浏览器窗口调整至至少 1080px。</p>
      </div>
      <div className="app-window">
        <aside className="nav-rail">
          <NavLink aria-label="GoIM 首页" className="nav-rail__brand" to="/app/chats"><Sparkles size={20} /></NavLink>
          <nav aria-label="主导航">
            {navItems.map((item) => {
              const Icon = item.icon;
              const active = location.pathname.startsWith(item.match);
              return (
                <NavLink aria-label={item.label} className={active ? "is-active" : ""} key={item.label} title={item.label} to={item.to}>
                  {active && <motion.span className="nav-rail__active" layoutId="nav-active" transition={{ duration: reduceMotion ? 0 : .24 }} />}
                  <Icon size={20} strokeWidth={1.8} />
                  <small>{item.label}</small>
                </NavLink>
              );
            })}
          </nav>
          <NavLink aria-label="个人设置" className="nav-rail__profile" to="/app/settings/profile"><Avatar name={user?.username ?? "用户"} online size="sm" src={user?.avatarUrl} /></NavLink>
        </aside>
        <div className="app-workspace"><Outlet /></div>
      </div>
    </div>
  );
}
