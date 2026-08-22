import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { BrowserRouter, Navigate, Route, Routes } from "react-router-dom";

import { LoginPage } from "../pages/LoginPage.tsx";
import { AccountPage } from "../pages/AccountPage.tsx";
import { WorkspacePage } from "../pages/WorkspacePage.tsx";
import { RequireAuth } from "../features/auth/RequireAuth.tsx";

const queryClient = new QueryClient({
  defaultOptions: {
    queries: { retry: 1, staleTime: 30_000, refetchOnWindowFocus: false },
    mutations: { retry: false },
  },
});

export function App() {
  return (
    <QueryClientProvider client={queryClient}>
      <BrowserRouter>
        <Routes>
		  <Route path="/" element={<RequireAuth><WorkspacePage /></RequireAuth>} />
		  <Route path="/account" element={<RequireAuth><AccountPage /></RequireAuth>} />
          <Route path="/login" element={<LoginPage />} />
          <Route path="*" element={<Navigate to="/" replace />} />
        </Routes>
      </BrowserRouter>
    </QueryClientProvider>
  );
}
