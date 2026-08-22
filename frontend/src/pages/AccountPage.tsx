import { useEffect, useState, type FormEvent } from "react";
import { ArrowLeft, Bell, Eye, KeyRound, LogOut, Save, UserRound } from "lucide-react";
import { Link, useNavigate } from "react-router-dom";

import { authApi } from "../features/auth/api.ts";
import { useAuthStore } from "../features/auth/store.ts";
import { ApiError } from "../shared/api/client.ts";
import type { UserSettings } from "../shared/types/domain.ts";

const defaultSettings: UserSettings = { userId: "", locale: "zh-CN", theme: "system", notificationEnabled: true, messagePreviewEnabled: true, createdAt: "", updatedAt: "" };

export function AccountPage() {
	const navigate = useNavigate();
	const user = useAuthStore((state) => state.user);
	const accessToken = useAuthStore((state) => state.accessToken)!;
	const setUser = useAuthStore((state) => state.setUser);
	const clearSession = useAuthStore((state) => state.clearSession);
	const [nickname, setNickname] = useState(user?.nickname ?? "");
	const [email, setEmail] = useState(user?.email ?? "");
	const [avatarUrl, setAvatarUrl] = useState(user?.avatarUrl ?? "");
	const [bio, setBio] = useState(user?.bio ?? "");
	const [settings, setSettings] = useState(defaultSettings);
	const [currentPassword, setCurrentPassword] = useState("");
	const [newPassword, setNewPassword] = useState("");
	const [message, setMessage] = useState("");
	const [error, setError] = useState("");

	useEffect(() => { authApi.getSettings(accessToken).then(setSettings).catch(handleError); }, [accessToken]);

	function handleError(caught: unknown) { setError(caught instanceof ApiError ? caught.message : "请求失败"); }
	function startAction() { setMessage(""); setError(""); }

	async function saveProfile(event: FormEvent) {
		event.preventDefault(); startAction();
		try { const updated = await authApi.updateMe(accessToken, { nickname, email, avatarUrl, bio }); setUser(updated); setMessage("个人资料已更新"); } catch (caught) { handleError(caught); }
	}

	async function saveSettings(event: FormEvent) {
		event.preventDefault(); startAction();
		try { setSettings(await authApi.updateSettings(accessToken, settings)); setMessage("偏好设置已更新"); } catch (caught) { handleError(caught); }
	}

	async function changePassword(event: FormEvent) {
		event.preventDefault(); startAction();
		try { await authApi.changePassword(accessToken, currentPassword, newPassword); clearSession(); navigate("/login", { replace: true }); } catch (caught) { handleError(caught); }
	}

	async function logout() {
		startAction();
		try { await authApi.logout(accessToken); } catch { /* Local logout still completes if the session already expired. */ }
		clearSession(); navigate("/login", { replace: true });
	}

	return (
		<main className="account-page">
			<header className="account-header"><div><Link to="/" className="back-link-static"><ArrowLeft size={16} /> 返回消息</Link><p className="eyebrow">ACCOUNT</p><h1>账号与设置</h1></div><button className="danger-button" type="button" onClick={logout}><LogOut size={17} />退出登录</button></header>
			{message ? <p className="account-notice">{message}</p> : null}{error ? <p className="form-error" role="alert">{error}</p> : null}
			<div className="account-layout">
				<form className="settings-section" onSubmit={saveProfile}><h2><UserRound size={18} />个人资料</h2><label><span>用户名</span><input value={user?.username ?? ""} disabled /></label><label><span>昵称</span><input value={nickname} maxLength={30} onChange={(event) => setNickname(event.target.value)} required /></label><label><span>邮箱</span><input type="email" value={email} onChange={(event) => setEmail(event.target.value)} /></label><label><span>头像 URL</span><input value={avatarUrl} onChange={(event) => setAvatarUrl(event.target.value)} /></label><label><span>个人简介</span><textarea value={bio} maxLength={280} rows={4} onChange={(event) => setBio(event.target.value)} /></label><button className="primary-button" type="submit"><Save size={17} />保存资料</button></form>
				<div className="settings-stack">
					<form className="settings-section" onSubmit={saveSettings}><h2><Bell size={18} />偏好设置</h2><label><span>语言</span><select value={settings.locale} onChange={(event) => setSettings({ ...settings, locale: event.target.value as UserSettings["locale"] })}><option value="zh-CN">简体中文</option><option value="en-US">English</option></select></label><label><span>主题</span><select value={settings.theme} onChange={(event) => setSettings({ ...settings, theme: event.target.value as UserSettings["theme"] })}><option value="system">跟随系统</option><option value="light">浅色</option><option value="dark">深色</option></select></label><label className="toggle-line"><span><Bell size={16} />消息通知</span><input type="checkbox" checked={settings.notificationEnabled} onChange={(event) => setSettings({ ...settings, notificationEnabled: event.target.checked })} /></label><label className="toggle-line"><span><Eye size={16} />消息预览</span><input type="checkbox" checked={settings.messagePreviewEnabled} onChange={(event) => setSettings({ ...settings, messagePreviewEnabled: event.target.checked })} /></label><button className="primary-button" type="submit"><Save size={17} />保存设置</button></form>
					<form className="settings-section" onSubmit={changePassword}><h2><KeyRound size={18} />修改密码</h2><label><span>当前密码</span><input type="password" autoComplete="current-password" value={currentPassword} onChange={(event) => setCurrentPassword(event.target.value)} required /></label><label><span>新密码</span><input type="password" minLength={8} maxLength={72} autoComplete="new-password" value={newPassword} onChange={(event) => setNewPassword(event.target.value)} required /></label><button className="primary-button" type="submit"><KeyRound size={17} />更新密码</button></form>
				</div>
			</div>
		</main>
	);
}

