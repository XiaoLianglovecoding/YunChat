import { useEffect, useState } from "react";
import { Navigate, Outlet, useLocation } from "react-router-dom";
import { Sparkles } from "lucide-react";
import { ensureFreshSession } from "../../lib/api";
import { useAuthStore } from "../../stores/authStore";

function SessionLoading() {
  return <main className="session-loading"><span><Sparkles size={21} /></span><p>正在恢复会话…</p></main>;
}

export function ProtectedRoute() {
  const location = useLocation();
  const refreshToken = useAuthStore((state) => state.refreshToken);
  const previewMode = useAuthStore((state) => state.previewMode);
  const previewAllowed = import.meta.env.DEV && previewMode;
  const [checking, setChecking] = useState(Boolean(refreshToken) && !previewAllowed);
  const [authenticated, setAuthenticated] = useState(previewAllowed);

  useEffect(() => {
    let active = true;
    if (previewAllowed) {
      setChecking(false);
      setAuthenticated(true);
      return;
    }
    if (!refreshToken) {
      setChecking(false);
      setAuthenticated(false);
      return;
    }
    setChecking(true);
    ensureFreshSession().then((valid) => {
      if (active) { setAuthenticated(valid); setChecking(false); }
    });
    return () => { active = false; };
  }, [previewAllowed, refreshToken]);

  if (checking) return <SessionLoading />;
  if (!authenticated) return <Navigate replace state={{ from: location.pathname }} to="/login" />;
  return <Outlet />;
}

export function GuestRoute() {
  const refreshToken = useAuthStore((state) => state.refreshToken);
  const previewMode = useAuthStore((state) => state.previewMode);
  if (refreshToken || (import.meta.env.DEV && previewMode)) return <Navigate replace to="/app/chats/lin-cheng" />;
  return <Outlet />;
}
