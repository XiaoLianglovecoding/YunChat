import type { UserSettings } from "../../goim-api-types";

interface NotificationPreferences {
  enabled: boolean;
  preview: boolean;
  mutedConversationIds: Set<string>;
}

let preferences: NotificationPreferences = { enabled: false, preview: true, mutedConversationIds: new Set() };
let audioContext: AudioContext | null = null;

function parseMuteList(value: string) {
  try {
    const parsed = JSON.parse(value) as unknown;
    return new Set(Array.isArray(parsed) ? parsed.filter((item): item is string => typeof item === "string") : []);
  } catch {
    return new Set<string>();
  }
}

export function configureNotifications(settings: Pick<UserSettings, "notification_enabled" | "msg_preview_enabled" | "mute_list">) {
  preferences = { enabled: settings.notification_enabled, preview: settings.msg_preview_enabled, mutedConversationIds: parseMuteList(settings.mute_list) };
}

export async function requestNotificationPermission() {
  if (!("Notification" in window)) return "unsupported" as const;
  if (Notification.permission === "granted") { unlockNotificationSound(); return "granted" as const; }
  const permission = await Notification.requestPermission();
  if (permission === "granted") unlockNotificationSound();
  return permission;
}

export function getNotificationPermission() {
  if (!("Notification" in window)) return "unsupported" as const;
  return Notification.permission;
}

export async function showTestNotification() {
  const permission = await requestNotificationPermission();
  if (permission !== "granted") return permission;
  const notification = new Notification("GoIM 测试通知", { body: "桌面通知已成功开启。", tag: `goim-test-${Date.now()}`, requireInteraction: true });
  notification.onclick = () => { window.focus(); notification.close(); };
  return permission;
}

export function unlockNotificationSound() {
  if (!window.AudioContext) return;
  audioContext ??= new AudioContext();
  if (audioContext.state === "suspended") void audioContext.resume();
}

function playNotificationSound() {
  unlockNotificationSound();
  if (!audioContext || audioContext.state !== "running") return;
  const oscillator = audioContext.createOscillator();
  const gain = audioContext.createGain();
  oscillator.type = "sine";
  oscillator.frequency.setValueAtTime(740, audioContext.currentTime);
  gain.gain.setValueAtTime(0.0001, audioContext.currentTime);
  gain.gain.exponentialRampToValueAtTime(0.12, audioContext.currentTime + 0.015);
  gain.gain.exponentialRampToValueAtTime(0.0001, audioContext.currentTime + 0.18);
  oscillator.connect(gain);
  gain.connect(audioContext.destination);
  oscillator.start();
  oscillator.stop(audioContext.currentTime + 0.2);
}

export function notifyIncomingMessage(input: { convId: string; title: string; content: string }) {
  if (!preferences.enabled || preferences.mutedConversationIds.has(input.convId)) return;
  playNotificationSound();
  if (!("Notification" in window) || Notification.permission !== "granted") return;
  if (document.visibilityState === "visible" && document.hasFocus()) return;
  const notification = new Notification(input.title, { body: preferences.preview ? input.content : "你收到了一条新消息", tag: `${input.convId}-${Date.now()}` });
  notification.onclick = () => { window.focus(); notification.close(); };
}
