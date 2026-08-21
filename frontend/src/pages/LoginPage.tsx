import { useState, type FormEvent } from "react";
import { ArrowLeft, ArrowRight, LockKeyhole, UserRound } from "lucide-react";
import { Link, useNavigate } from "react-router-dom";

import { authApi } from "../features/auth/api.ts";
import { useAuthStore } from "../features/auth/store.ts";
import { ApiError } from "../shared/api/client.ts";

export function LoginPage() {
  const navigate = useNavigate();
  const setSession = useAuthStore((state) => state.setSession);
  const [login, setLogin] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [submitting, setSubmitting] = useState(false);

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setError("");
    setSubmitting(true);
    try {
      // TODO(linknest): replace the temporary device key when device enrollment is implemented.
      const session = await authApi.login({
        login,
        password,
        deviceKey: "web-scaffold",
        deviceName: navigator.userAgent,
        platform: "web",
      });
      setSession(session.user, session.accessToken, session.refreshToken);
      navigate("/");
    } catch (caught) {
      setError(caught instanceof ApiError ? caught.message : "暂时无法登录");
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <main className="auth-page">
      <section className="auth-panel">
        <Link className="back-link" to="/"><ArrowLeft size={16} /> 返回工作台</Link>
        <div className="auth-brand"><span className="brand-mark">LN</span><span>LinkNest IM</span></div>
        <header><p className="eyebrow">WELCOME BACK</p><h1>登录</h1><p>继续处理你的会话与联系人。</p></header>
        <form onSubmit={handleSubmit}>
          <label><span>账号</span><div className="auth-input"><UserRound size={18} /><input autoComplete="username" value={login} onChange={(event) => setLogin(event.target.value)} required /></div></label>
          <label><span>密码</span><div className="auth-input"><LockKeyhole size={18} /><input type="password" autoComplete="current-password" value={password} onChange={(event) => setPassword(event.target.value)} required /></div></label>
          {error ? <p className="form-error" role="alert">{error}</p> : null}
          <button className="primary-button" type="submit" disabled={submitting}>{submitting ? "登录中" : "登录"}<ArrowRight size={18} /></button>
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

