import { motion, useReducedMotion } from "framer-motion";
import { LockKeyhole, Sparkles, UserRound } from "lucide-react";
import { useState, type FormEvent } from "react";
import { Link, useLocation, useNavigate } from "react-router-dom";
import { ApiError } from "../api/client";
import { Button, TextField } from "../components/ui";
import { authApi } from "../lib/api";
import { queryClient } from "../lib/queryClient";
import { useAuthStore } from "../stores/authStore";

interface AuthPageProps {
  mode: "login" | "register";
}

export function AuthPage({ mode }: AuthPageProps) {
  const navigate = useNavigate();
  const location = useLocation();
  const reduceMotion = useReducedMotion();
  const [loading, setLoading] = useState(false);
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const setSession = useAuthStore((state) => state.setSession);
  const enterPreview = useAuthStore((state) => state.enterPreview);
  const isLogin = mode === "login";

  const handleSubmit = async (event: FormEvent) => {
    event.preventDefault();
    setError(null);
    if (!isLogin && password !== confirmPassword) {
      setError("两次输入的密码不一致");
      return;
    }
    setLoading(true);
    try {
      if (!isLogin) await authApi.register({ username: username.trim(), password });
      const session = await authApi.login({ username: username.trim(), password });
      setSession(session, username.trim());
      queryClient.clear();
      const destination = (location.state as { from?: string } | null)?.from ?? "/app/chats/lin-cheng";
      navigate(destination, { replace: true });
    } catch (requestError) {
      if (requestError instanceof ApiError) setError(requestError.message || "认证失败，请检查输入");
      else setError("无法连接服务器，请确认后端服务已启动");
    } finally {
      setLoading(false);
    }
  };

  return (
    <main className="auth-page">
      <div className="auth-orb auth-orb--one" />
      <div className="auth-orb auth-orb--two" />
      <motion.section
        animate={{ opacity: 1, y: 0 }}
        className="auth-card"
        initial={{ opacity: 0, y: reduceMotion ? 0 : 12 }}
        transition={{ duration: reduceMotion ? 0 : .42 }}
      >
        <header>
          <span className="auth-logo"><Sparkles size={21} /></span>
          <h1>{isLogin ? "欢迎回来" : "创建你的账户"}</h1>
          <p>{isLogin ? "继续你的对话，保持每一段连接。" : "从一段简单、清晰的对话开始。"}</p>
        </header>
        <form onSubmit={handleSubmit}>
          <TextField autoComplete="username" disabled={loading} label="用户名" leadingIcon={<UserRound size={16} />} minLength={3} onChange={(event) => setUsername(event.target.value)} placeholder="输入用户名" required value={username} />
          <TextField autoComplete={isLogin ? "current-password" : "new-password"} disabled={loading} label="密码" leadingIcon={<LockKeyhole size={16} />} minLength={6} onChange={(event) => setPassword(event.target.value)} placeholder="至少 6 位字符" required type="password" value={password} />
          {!isLogin && <TextField autoComplete="new-password" disabled={loading} label="确认密码" leadingIcon={<LockKeyhole size={16} />} onChange={(event) => setConfirmPassword(event.target.value)} placeholder="再次输入密码" required type="password" value={confirmPassword} />}
          {error && <div className="auth-error" role="alert">{error}</div>}
          <Button loading={loading} size="lg" type="submit">{isLogin ? "登录" : "注册"}</Button>
        </form>
        <footer>
          <span>{isLogin ? "还没有账户？" : "已经有账户？"}</span>
          <Link to={isLogin ? "/register" : "/login"}>{isLogin ? "创建账户" : "返回登录"}</Link>
        </footer>
        {import.meta.env.DEV && <button className="auth-preview-button" onClick={() => { enterPreview(); navigate("/app/chats/lin-cheng", { replace: true }); }} type="button">暂不连接后端，进入界面预览</button>}
      </motion.section>
      <p className="auth-caption">GoIM · 专注而轻盈的即时通讯体验</p>
    </main>
  );
}
