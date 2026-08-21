export type RealtimeEvent<T = unknown> = {
  id: string;
  type: string;
  timestamp: number;
  data?: T;
};

type Listener = (event: RealtimeEvent) => void;

export class RealtimeClient {
  private socket: WebSocket | null = null;
  private reconnectAttempt = 0;
  private reconnectTimer: number | null = null;
  private listeners = new Map<string, Set<Listener>>();
  private manuallyClosed = false;

  constructor(private readonly url: string) {}

  connect(): void {
    if (this.socket?.readyState === WebSocket.OPEN || this.socket?.readyState === WebSocket.CONNECTING) return;
    this.manuallyClosed = false;
    this.socket = new WebSocket(this.url);
    this.socket.addEventListener("open", () => {
      this.reconnectAttempt = 0;
      this.emitLocal("connection.open", {});
      // TODO(linknest): send the authenticated protocol handshake as the first frame.
    });
    this.socket.addEventListener("message", (message) => this.handleMessage(message.data));
    this.socket.addEventListener("close", () => {
      this.socket = null;
      this.emitLocal("connection.close", {});
      if (!this.manuallyClosed) this.scheduleReconnect();
    });
  }

  close(): void {
    this.manuallyClosed = true;
    if (this.reconnectTimer !== null) window.clearTimeout(this.reconnectTimer);
    this.reconnectTimer = null;
    this.socket?.close(1000, "client closed");
    this.socket = null;
  }

  send<T>(event: RealtimeEvent<T>): boolean {
    if (this.socket?.readyState !== WebSocket.OPEN) return false;
    this.socket.send(JSON.stringify(event));
    return true;
  }

  subscribe(type: string, listener: Listener): () => void {
    const listeners = this.listeners.get(type) ?? new Set<Listener>();
    listeners.add(listener);
    this.listeners.set(type, listeners);
    return () => {
      listeners.delete(listener);
      if (listeners.size === 0) this.listeners.delete(type);
    };
  }

  private handleMessage(raw: unknown): void {
    if (typeof raw !== "string") return;
    try {
      const event = JSON.parse(raw) as RealtimeEvent;
      if (!event.id || !event.type || typeof event.timestamp !== "number") return;
      this.dispatch(event);
    } catch {
      this.emitLocal("protocol.error", { reason: "invalid_json" });
    }
  }

  private emitLocal(type: string, data: unknown): void {
    this.dispatch({ id: crypto.randomUUID(), type, timestamp: Date.now(), data });
  }

  private dispatch(event: RealtimeEvent): void {
    this.listeners.get(event.type)?.forEach((listener) => listener(event));
    this.listeners.get("*")?.forEach((listener) => listener(event));
  }

  private scheduleReconnect(): void {
    const baseDelay = Math.min(30_000, 1_000 * 2 ** this.reconnectAttempt++);
    const jitter = Math.floor(Math.random() * 500);
    this.reconnectTimer = window.setTimeout(() => this.connect(), baseDelay + jitter);
  }
}

