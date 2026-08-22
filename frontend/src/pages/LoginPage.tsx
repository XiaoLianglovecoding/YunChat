import { useState, type FormEvent } from "react";
import { ArrowRight, LockKeyhole, Mail, UserRound } from "lucide-react";
import { Navigate, useNavigate } from "react-router-dom";

import { authApi, getDeviceIdentity } from "../features/auth/api.ts";
import { useAuthStore } from "../features/auth/store.ts";
import { ApiError } from "../shared/api/client.ts";

type Mode = "login" | "register";

export function LoginPage() {
	const navigate = useNavigate();
	const existingRefreshToken = useAuthStore((state) => state.refreshToken);
	const setSession = useAuthStore((state) => state.setSession);
	const [mode, setMode] = useState<Mode>("login");
	const [login, setLogin] = useState("");
	const [username, setUsername] = useState("");
	const [nickname, setNickname] = useState("");
	const [email, setEmail] = useState("");
	const [password, setPassword] = useState("");
	const [error, setError] = useState("");
	const [submitting, setSubmitting] = useState(false);

	if (existingRefreshToken) return <Navigate to="/" replace />;

	async function handleSubmit(event: FormEvent<HTMLFormElement>) {
		event.preventDefault();
		setError("");
		setSubmitting(true);
		try {
			const device = getDeviceIdentity();
			const session = mode === "login"
				? await authApi.login({ login, password, ...device })
				: await authApi.register({ username, nickname, email: email || undefined, password, ...device });
			setSession(session.user, session.accessToken, session.refreshToken);
			navigate("/");
		} catch (caught) {
			setError(caught instanceof ApiError ? caught.message : "暂时无法完成身份验证");
		} finally {
			setSubmitting(false);
		}
	}

	return (
		<main className="auth-page">
			<section className="auth-panel">
				<div className="auth-brand"><span className="brand-mark">LN</span><span>LinkNest IM</span></div>
				<div className="auth-tabs" role="tablist" aria-label="身份验证方式">
					<button type="button" className={mode === "login" ? "active" : ""} onClick={() => { setMode("login"); setError(""); }}>登录</button>
					<button type="button" className={mode === "register" ? "active" : ""} onClick={() => { setMode("register"); setError(""); }}>注册</button>
				</div>
				<header><p className="eyebrow">{mode === "login" ? "WELCOME BACK" : "CREATE ACCOUNT"}</p><h1>{mode === "login" ? "登录" : "创建账号"}</h1><p>{mode === "login" ? "继续处理你的会话与联系人。" : "建立你的 LinkNest 开发者身份。"}</p></header>
				<form onSubmit={handleSubmit}>
					{mode === "login" ? (
						<label><span>账号</span><div className="auth-input"><UserRound size={18} /><input autoComplete="username" value={login} onChange={(event) => setLogin(event.target.value)} required placeholder="用户名或邮箱" /></div></label>
					) : (
						<>
							<label><span>用户名</span><div className="auth-input"><UserRound size={18} /><input autoComplete="username" minLength={4} maxLength={32} value={username} onChange={(event) => setUsername(event.target.value)} required placeholder="字母开头，支持数字和下划线" /></div></label>
							<label><span>昵称</span><div className="auth-input"><UserRound size={18} /><input maxLength={30} value={nickname} onChange={(event) => setNickname(event.target.value)} required /></div></label>
							<label><span>邮箱（可选）</span><div className="auth-input"><Mail size={18} /><input type="email" autoComplete="email" value={email} onChange={(event) => setEmail(event.target.value)} /></div></label>
						</>
					)}
					<label><span>密码</span><div className="auth-input"><LockKeyhole size={18} /><input type="password" minLength={8} maxLength={72} autoComplete={mode === "login" ? "current-password" : "new-password"} value={password} onChange={(event) => setPassword(event.target.value)} required /></div></label>
					{error ? <p className="form-error" role="alert">{error}</p> : null}
					<button className="primary-button" type="submit" disabled={submitting}>{submitting ? "处理中" : mode === "login" ? "登录" : "注册并登录"}<ArrowRight size={18} /></button>
				</form>
			</section>
			<aside className="auth-context" aria-label="产品标识">
				<span className="context-index">01</span>
				<div><p>LINK YOUR PEOPLE.</p><p>KEEP THE CONTEXT.</p></div>
				<span className="context-caption">Private workspace · LinkNest</span>
			</aside>
		</main>
	);
}

