import { Bell, ChevronRight, Info, LogOut, MessageSquareText, ShieldCheck, UserRound } from "lucide-react";
import { useEffect, useRef, useState, type ChangeEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { NavLink, useNavigate, useParams } from "react-router-dom";
import { ApiError } from "../api/client";
import { Avatar, Badge, Button, Switch } from "../components/ui";
import type { UpdateSettingsRequest } from "../../goim-api-types";
import { accountApi, settingsApi, uploadApi } from "../lib/api";
import { useChatStore } from "../stores/chatStore";
import { useAuthStore } from "../stores/authStore";
import { configureNotifications, getNotificationPermission, requestNotificationPermission, showTestNotification, unlockNotificationSound } from "../realtime/notifications";

const sections = [
  { id: "profile", label: "个人资料", icon: UserRound },
  { id: "notifications", label: "通知", icon: Bell },
  { id: "chat", label: "聊天设置", icon: MessageSquareText },
  { id: "privacy", label: "隐私与安全", icon: ShieldCheck },
  { id: "about", label: "关于", icon: Info },
];

export function SettingsPage() {
  const { section = "profile" } = useParams();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const user = useAuthStore((state) => state.user);
  const clearSession = useAuthStore((state) => state.clearSession);
  const previewMode = useAuthStore((state) => state.previewMode);
  const setAvatarUrl = useAuthStore((state) => state.setAvatarUrl);
  const setSession = useAuthStore((state) => state.setSession);
  const conversations = useChatStore((state) => state.conversations);
  const setConversationMuted = useChatStore((state) => state.setConversationMuted);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const [uploading, setUploading] = useState(false);
  const [uploadError, setUploadError] = useState<string | null>(null);
  const [notifications, setNotifications] = useState(true);
  const [preview, setPreview] = useState(true);
  const [notificationPermissionError, setNotificationPermissionError] = useState<string | null>(null);
  const [notificationPermission, setNotificationPermission] = useState(() => getNotificationPermission());
  const [usernameDraft, setUsernameDraft] = useState(user?.username ?? "");
  const [currentPassword, setCurrentPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [accountError, setAccountError] = useState<string | null>(null);
  const [accountSuccess, setAccountSuccess] = useState<string | null>(null);
  const [accountSaving, setAccountSaving] = useState(false);
  const settingsQuery = useQuery({ queryKey: ["settings"], queryFn: settingsApi.get, enabled: !previewMode });
  const settingsMutation = useMutation({
    mutationFn: (input: UpdateSettingsRequest) => settingsApi.update(input),
    onSuccess: (_data, input) => {
      queryClient.setQueryData(["settings"], (current: typeof settingsQuery.data) => current ? {
        ...current,
        notification_enabled: input.notification_enabled,
        msg_preview_enabled: input.msg_preview_enabled,
        mute_list: input.mute_list,
      } : current);
    },
    onError: () => {
      if (!settingsQuery.data) return;
      setNotifications(settingsQuery.data.notification_enabled);
      setPreview(settingsQuery.data.msg_preview_enabled);
    },
  });

  useEffect(() => {
    if (!settingsQuery.data) return;
    setNotifications(settingsQuery.data.notification_enabled);
    setPreview(settingsQuery.data.msg_preview_enabled);
    configureNotifications(settingsQuery.data);
    if (settingsQuery.data.notification_enabled) unlockNotificationSound();
  }, [settingsQuery.data]);

  useEffect(() => { setUsernameDraft(user?.username ?? ""); }, [user?.username]);

  const updatePreferences = async (nextNotifications: boolean, nextPreview: boolean) => {
    if (nextNotifications && !previewMode) {
      const permission = await requestNotificationPermission();
      setNotificationPermission(permission);
      if (permission !== "granted") {
        setNotificationPermissionError(permission === "unsupported" ? "当前浏览器不支持桌面通知" : "浏览器通知权限未开启，请在地址栏的网站权限中允许通知");
        return;
      }
    }
    setNotificationPermissionError(null);
    setNotifications(nextNotifications);
    setPreview(nextPreview);
    const nextSettings = {
      notification_enabled: nextNotifications,
      msg_preview_enabled: nextPreview,
      mute_list: settingsQuery.data?.mute_list ?? "[]",
    };
    configureNotifications(nextSettings);
    settingsMutation.mutate(nextSettings);
  };

  const testDesktopNotification = async () => {
    const permission = await showTestNotification();
    setNotificationPermission(permission);
    setNotificationPermissionError(permission === "granted" ? null : permission === "unsupported" ? "当前浏览器不支持桌面通知" : "浏览器通知权限未开启，请点击地址栏左侧的网站权限并允许通知");
  };

  const logout = () => {
    clearSession();
    queryClient.clear();
    navigate("/login", { replace: true });
  };

  const uploadAvatar = async (event: ChangeEvent<HTMLInputElement>) => {
    const file = event.target.files?.[0];
    event.target.value = "";
    if (!file) return;
    if (!/\.(jpe?g|png|gif)$/i.test(file.name)) { setUploadError("请选择 JPG、PNG 或 GIF 图片"); return; }
    setUploadError(null);
    if (previewMode) { setAvatarUrl(URL.createObjectURL(file)); return; }
    setUploading(true);
    try {
      const response = await uploadApi.avatar(file);
      setAvatarUrl(response.url);
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ["friends"] }),
        queryClient.invalidateQueries({ queryKey: ["moments"] }),
        queryClient.invalidateQueries({ queryKey: ["group-members"] }),
      ]);
    }
    catch { setUploadError("头像上传失败，请稍后重试"); }
    finally { setUploading(false); }
  };

  const updateUsername = async () => {
    const username = usernameDraft.trim();
    setAccountError(null);
    setAccountSuccess(null);
    if (previewMode) { setAccountError("预览模式不支持修改账户信息"); return; }
    if (username.length < 3 || username.length > 50) { setAccountError("用户名必须为 3-50 个字符"); return; }
    setAccountSaving(true);
    try {
      const session = await accountApi.updateUsername({ username });
      setSession(session, username);
      setAccountSuccess("用户名修改成功");
    } catch (failure) {
      setAccountError(failure instanceof ApiError ? failure.message : "用户名修改失败，请稍后重试");
    } finally { setAccountSaving(false); }
  };

  const updatePassword = async () => {
    setAccountError(null);
    setAccountSuccess(null);
    if (previewMode) { setAccountError("预览模式不支持修改账户信息"); return; }
    if (newPassword.length < 6) { setAccountError("新密码必须至少为 6 个字符"); return; }
    if (newPassword !== confirmPassword) { setAccountError("两次输入的新密码不一致"); return; }
    setAccountSaving(true);
    try {
      await accountApi.updatePassword({ current_password: currentPassword, new_password: newPassword });
      setCurrentPassword("");
      setNewPassword("");
      setConfirmPassword("");
      setAccountSuccess("密码修改成功；下次登录请使用新密码。");
    } catch (failure) {
      setAccountError(failure instanceof ApiError ? failure.message : "密码修改失败，请稍后重试");
    } finally { setAccountSaving(false); }
  };

  const parseMuteList = () => { try { const parsed = JSON.parse(settingsQuery.data?.mute_list ?? "[]"); return Array.isArray(parsed) ? parsed as string[] : []; } catch { return []; } };
  const mutedIds = parseMuteList();
  const toggleMute = async (convId: string, muted: boolean) => {
    if (previewMode) { setConversationMuted(convId, !muted); return; }
    muted ? await settingsApi.unmute(convId) : await settingsApi.mute({ convId });
    await queryClient.invalidateQueries({ queryKey: ["settings"] });
    setConversationMuted(convId, !muted);
  };

  return (
    <>
      <aside className="module-sidebar settings-sidebar">
        <header className="module-sidebar__header"><div><span className="eyebrow">Preferences</span><h1>设置</h1></div></header>
        <nav aria-label="设置分类" className="settings-nav">{sections.map((item) => { const Icon = item.icon; return <NavLink className={({ isActive }) => isActive ? "is-active" : ""} key={item.id} to={`/app/settings/${item.id}`}><Icon size={17} /><span>{item.label}</span><ChevronRight size={15} /></NavLink>; })}</nav>
        <button className="logout-button" onClick={logout}><LogOut size={17} />退出登录</button>
      </aside>

      <section className="module-main settings-main">
        {section === "profile" && <SettingsSection description="管理你的头像和账户基础信息。" title="个人资料"><div className="profile-setting"><Avatar name={user?.username ?? "用户"} online size="xl" src={user?.avatarUrl} /><div><strong>个人头像</strong><p>支持 JPG、PNG 或 GIF，最大 50MB。</p><input accept="image/jpeg,image/png,image/gif" hidden onChange={(event) => void uploadAvatar(event)} ref={fileInputRef} type="file" /><Button loading={uploading} onClick={() => fileInputRef.current?.click()} size="sm" variant="secondary">更换头像</Button>{uploadError && <span className="profile-upload-error">{uploadError}</span>}</div></div><div className="account-settings"><section><header><h3>用户名</h3><p>用户名全局唯一，修改后将立即在会话中生效。</p></header><div className="account-settings__form"><input aria-label="新用户名" autoComplete="username" disabled={accountSaving} maxLength={50} onChange={(event) => setUsernameDraft(event.target.value)} value={usernameDraft} /><Button disabled={accountSaving || usernameDraft.trim() === user?.username} loading={accountSaving} onClick={() => void updateUsername()} size="sm" variant="secondary">保存用户名</Button></div></section><section><header><h3>修改密码</h3><p>修改时需要验证当前密码；新密码至少 6 个字符。</p></header><div className="account-settings__form account-settings__form--password"><input aria-label="当前密码" autoComplete="current-password" disabled={accountSaving} onChange={(event) => setCurrentPassword(event.target.value)} placeholder="当前密码" type="password" value={currentPassword} /><input aria-label="新密码" autoComplete="new-password" disabled={accountSaving} minLength={6} onChange={(event) => setNewPassword(event.target.value)} placeholder="新密码" type="password" value={newPassword} /><input aria-label="确认新密码" autoComplete="new-password" disabled={accountSaving} minLength={6} onChange={(event) => setConfirmPassword(event.target.value)} placeholder="确认新密码" type="password" value={confirmPassword} /><Button disabled={accountSaving || !currentPassword || !newPassword || !confirmPassword} loading={accountSaving} onClick={() => void updatePassword()} size="sm" variant="secondary">修改密码</Button></div></section>{accountError && <p className="account-settings__message is-error">{accountError}</p>}{accountSuccess && <p className="account-settings__message is-success">{accountSuccess}</p>}</div><SettingRow label="用户 ID" value={user?.id ? `#${user.id}` : "—"} /></SettingsSection>}
        {section === "notifications" && <SettingsSection description="选择何时以及如何接收新消息提醒。" title="通知">{previewMode && <div className="settings-preview-note">预览模式下设置仅在当前页面生效，不会写入服务器。</div>}{settingsQuery.isError && <div className="settings-sync-error">暂时无法读取服务端设置，请检查后端连接后重试。</div>}<div className="settings-group"><Switch checked={notifications} description="收到新消息时播放提示音；页面在后台时显示桌面通知" disabled={settingsQuery.isLoading || settingsMutation.isPending} label="消息通知" onCheckedChange={(checked) => previewMode ? setNotifications(checked) : void updatePreferences(checked, preview)} /><Switch checked={preview} description="在系统通知中显示消息内容" disabled={settingsQuery.isLoading || settingsMutation.isPending} label="消息预览" onCheckedChange={(checked) => previewMode ? setPreview(checked) : void updatePreferences(notifications, checked)} /></div><div className="notification-permission-row"><span>浏览器通知权限：<strong>{notificationPermission === "granted" ? "已允许" : notificationPermission === "denied" ? "已阻止" : notificationPermission === "default" ? "等待授权" : "不支持"}</strong></span><Button onClick={() => void testDesktopNotification()} size="sm" variant="secondary">测试桌面通知</Button></div>{notificationPermissionError && <p className="settings-save-error">{notificationPermissionError}</p>}{settingsMutation.isError && <p className="settings-save-error">保存失败，当前显示可能尚未同步到服务器。</p>}</SettingsSection>}
        {section === "chat" && <SettingsSection description="管理会话提醒和输入行为。" title="聊天设置"><div className="settings-group"><Switch checked description="按 Enter 发送，Shift + Enter 换行" label="快捷键发送" onCheckedChange={() => undefined} /><div className="muted-row"><div><strong>免打扰会话</strong><p>这些会话不会触发声音与桌面通知。</p></div><Badge>{previewMode ? conversations.filter((item) => item.muted).length : mutedIds.length} 个会话</Badge></div></div><div className="mute-conversation-list">{conversations.map((conversation) => { const muted = previewMode ? Boolean(conversation.muted) : mutedIds.includes(conversation.id); return <div key={conversation.id}><Avatar name={conversation.name} size="sm" /><span><strong>{conversation.name}</strong><small>{conversation.group ? "群聊" : "私聊"}</small></span><Switch checked={muted} label={`${conversation.name}免打扰`} onCheckedChange={() => void toggleMute(conversation.id, muted)} /></div>; })}</div></SettingsSection>}
        {section === "privacy" && <SettingsSection description="了解当前账户的安全与隐私策略。" title="隐私与安全"><div className="settings-callout"><ShieldCheck size={20} /><div><strong>单设备登录保护已开启</strong><p>新设备登录后，旧设备会自动退出，以保护账户安全。</p></div></div></SettingsSection>}
        {section === "about" && <SettingsSection description="关于当前 GoIM Web 客户端。" title="关于"><div className="about-mark"><span><Info size={24} /></span><h3>GoIM</h3><p>Version 0.1.0 · Phase 02</p></div></SettingsSection>}
      </section>
    </>
  );
}

function SettingsSection({ title, description, children }: { title: string; description: string; children: React.ReactNode }) {
  return <div className="settings-section"><header><h2>{title}</h2><p>{description}</p></header>{children}</div>;
}

function SettingRow({ label, value }: { label: string; value: string }) {
  return <div className="setting-row"><span>{label}</span><strong>{value}</strong></div>;
}
