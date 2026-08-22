import { useEffect, useState, type ReactNode } from "react";
import { Navigate } from "react-router-dom";

import { authApi } from "./api.ts";
import { useAuthStore } from "./store.ts";

export function RequireAuth({ children }: { children: ReactNode }) {
	const accessToken = useAuthStore((state) => state.accessToken);
	const refreshToken = useAuthStore((state) => state.refreshToken);
	const setSession = useAuthStore((state) => state.setSession);
	const clearSession = useAuthStore((state) => state.clearSession);
	const [restoring, setRestoring] = useState(Boolean(!accessToken && refreshToken));

	useEffect(() => {
		if (accessToken || !refreshToken) { setRestoring(false); return; }
		let active = true;
		authApi.refresh(refreshToken).then((session) => {
			if (active) setSession(session.user, session.accessToken, session.refreshToken);
		}).catch(() => {
			if (active) clearSession();
		}).finally(() => { if (active) setRestoring(false); });
		return () => { active = false; };
	}, [accessToken, refreshToken, setSession, clearSession]);

	if (restoring) return <main className="session-loading">正在恢复会话...</main>;
	if (!accessToken) return <Navigate to="/login" replace />;
	return children;
}
