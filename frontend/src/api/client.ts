import type { ApiResponse } from "../../goim-api-types";

export interface ApiClientOptions {
  baseUrl: string;
  getAccessToken?: () => string | null | Promise<string | null>;
  /** Return true after recovering the session to retry the request once. */
  onUnauthorized?: () => boolean | Promise<boolean>;
  fetch?: typeof fetch;
  timeoutMs?: number;
}

export class ApiError extends Error {
  constructor(
    message: string,
    readonly code: number,
    readonly status: number,
  ) {
    super(message);
    this.name = "ApiError";
  }
}

export class GoIMApiClient {
  private readonly baseUrl: string;
  private readonly fetchImpl: typeof fetch;
  private readonly timeoutMs: number;

  constructor(private readonly options: ApiClientOptions) {
    this.baseUrl = options.baseUrl.replace(/\/$/, "");
    this.fetchImpl = options.fetch ?? globalThis.fetch.bind(globalThis);
    this.timeoutMs = options.timeoutMs ?? 15_000;
  }

  async request<T>(path: string, init: RequestInit = {}, mayRetry = true): Promise<T> {
    const token = await this.options.getAccessToken?.();
    const headers = new Headers(init.headers);
    if (token) headers.set("Authorization", `Bearer ${token}`);

    const controller = new AbortController();
    const relayAbort = () => controller.abort();
    init.signal?.addEventListener("abort", relayAbort, { once: true });
    const timeout = globalThis.setTimeout(() => controller.abort(), this.timeoutMs);
    let response: Response;
    try {
      response = await this.fetchImpl(`${this.baseUrl}${path}`, { ...init, headers, signal: controller.signal });
    } catch {
      const message = controller.signal.aborted ? "请求超时，请稍后重试" : "网络连接失败，请检查后端服务";
      throw new ApiError(message, -1, 0);
    } finally {
      globalThis.clearTimeout(timeout);
      init.signal?.removeEventListener("abort", relayAbort);
    }
    const isJson = response.headers.get("content-type")?.includes("application/json");
    let body: ApiResponse<T> | undefined;
    if (isJson) {
      try {
        body = await response.json() as ApiResponse<T>;
      } catch {
        body = undefined;
      }
    }

    if (response.status === 401 && mayRetry && this.options.onUnauthorized) {
      const recovered = await this.options.onUnauthorized();
      if (recovered) return this.request<T>(path, init, false);
    }
    if (!response.ok || !body || body.code !== 0) {
      throw new ApiError(body?.message || response.statusText || "请求失败，请稍后重试", body?.code ?? -1, response.status);
    }
    return body.data as T;
  }

  get<T>(path: string): Promise<T> { return this.request<T>(path); }
  post<T>(path: string, body?: unknown): Promise<T> {
    return this.request<T>(path, {
      method: "POST",
      headers: body === undefined ? undefined : { "Content-Type": "application/json" },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
  }
  put<T>(path: string, body: unknown): Promise<T> {
    return this.request<T>(path, { method: "PUT", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) });
  }
  delete<T>(path: string): Promise<T> { return this.request<T>(path, { method: "DELETE" }); }
  upload<T>(path: string, form: FormData): Promise<T> { return this.request<T>(path, { method: "POST", body: form }); }
}
