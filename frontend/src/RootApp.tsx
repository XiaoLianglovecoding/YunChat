import { QueryClientProvider } from "@tanstack/react-query";
import { lazy, Suspense } from "react";
import { BrowserRouter, Navigate, Route, Routes } from "react-router-dom";
import { GuestRoute, ProtectedRoute } from "./components/auth/RouteGuards";
import { ErrorBoundary } from "./components/system/ErrorBoundary";
import { PageLoading } from "./components/system/PageLoading";
import { queryClient } from "./lib/queryClient";

const AppShell = lazy(() => import("./components/layout/AppShell").then((module) => ({ default: module.AppShell })));
const AuthPage = lazy(() => import("./pages/AuthPage").then((module) => ({ default: module.AuthPage })));
const ChatPage = lazy(() => import("./pages/ChatPage").then((module) => ({ default: module.ChatPage })));
const ContactsPage = lazy(() => import("./pages/ContactsPage").then((module) => ({ default: module.ContactsPage })));
const MomentsPage = lazy(() => import("./pages/MomentsPage").then((module) => ({ default: module.MomentsPage })));
const SettingsPage = lazy(() => import("./pages/SettingsPage").then((module) => ({ default: module.SettingsPage })));

export default function RootApp() {
  return <ErrorBoundary><QueryClientProvider client={queryClient}><BrowserRouter><Suspense fallback={<PageLoading />}><Routes>
    <Route element={<GuestRoute />}><Route element={<AuthPage mode="login" />} path="/login" /><Route element={<AuthPage mode="register" />} path="/register" /></Route>
    <Route element={<ProtectedRoute />}><Route element={<AppShell />} path="/app"><Route element={<Navigate replace to="chats" />} index /><Route element={<ChatPage />} path="chats/:conversationId?" /><Route element={<ContactsPage />} path="contacts/:contactId?" /><Route element={<MomentsPage />} path="moments" /><Route element={<SettingsPage />} path="settings/:section?" /></Route></Route>
    <Route element={<Navigate replace to="/app/chats" />} path="*" />
  </Routes></Suspense></BrowserRouter></QueryClientProvider></ErrorBoundary>;
}
