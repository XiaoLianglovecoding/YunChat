import type { ClientWsMessage, ServerWsMessage } from "../../goim-ws-types";
import { env } from "../config/env";

export type ConnectionState = "idle" | "connecting" | "connected" | "reconnecting" | "offline";

interface SocketHandlers {
  onStateChange: (state: ConnectionState) => void;
  onMessage: (message: ServerWsMessage) => void;
}

const noopHandlers: SocketHandlers = {
  onStateChange: () => undefined,
  onMessage: () => undefined,
};

class GoIMSocket {
  private socket: WebSocket | null = null;
  private token: string | null = null;
  private reconnectTimer: number | null = null;
  private reconnectAttempt = 0;
  private explicitlyClosed = true;
  private handlers = noopHandlers;

  setHandlers(handlers: SocketHandlers) {
    this.handlers = handlers;
  }

  connect(token: string) {
    if (this.token === token && (this.socket?.readyState === WebSocket.OPEN || this.socket?.readyState === WebSocket.CONNECTING)) return;
    this.disconnect(false);
    this.token = token;
    this.explicitlyClosed = false;
    this.open(false);
  }

  disconnect(clearToken = true) {
    this.explicitlyClosed = true;
    if (this.reconnectTimer !== null) window.clearTimeout(this.reconnectTimer);
    this.reconnectTimer = null;
    this.socket?.close(1000, "client_close");
    this.socket = null;
    if (clearToken) this.token = null;
    this.reconnectAttempt = 0;
    this.handlers.onStateChange("idle");
  }

  send(message: ClientWsMessage) {
    if (this.socket?.readyState !== WebSocket.OPEN) return false;
    this.socket.send(JSON.stringify(message));
    return true;
  }

  private open(reconnecting: boolean) {
    if (!this.token || this.explicitlyClosed) return;
    this.handlers.onStateChange(reconnecting ? "reconnecting" : "connecting");
    const socket = new WebSocket(`${env.wsUrl}?token=${encodeURIComponent(this.token)}`);
    this.socket = socket;

    socket.onopen = () => {
      if (this.socket !== socket) return;
      this.reconnectAttempt = 0;
      this.handlers.onStateChange("connected");
    };

    socket.onmessage = (event) => {
      if (this.socket !== socket || typeof event.data !== "string") return;
      try {
        const message = JSON.parse(event.data) as ServerWsMessage;
        if (message && typeof message === "object" && "type" in message) this.handlers.onMessage(message);
      } catch {
        // Ignore malformed application frames; the next valid frame remains usable.
      }
    };

    socket.onerror = () => {
      if (this.socket === socket) this.handlers.onStateChange("offline");
    };

    socket.onclose = () => {
      if (this.socket !== socket) return;
      this.socket = null;
      if (this.explicitlyClosed || !this.token) {
        this.handlers.onStateChange("idle");
        return;
      }
      this.scheduleReconnect();
    };
  }

  private scheduleReconnect() {
    this.handlers.onStateChange("reconnecting");
    const exponentialDelay = Math.min(30_000, 1_000 * 2 ** this.reconnectAttempt);
    const jitter = Math.floor(Math.random() * 350);
    this.reconnectAttempt += 1;
    this.reconnectTimer = window.setTimeout(() => {
      this.reconnectTimer = null;
      this.open(true);
    }, exponentialDelay + jitter);
  }
}

export const goimSocket = new GoIMSocket();
