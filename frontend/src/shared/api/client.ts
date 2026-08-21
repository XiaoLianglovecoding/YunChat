import type { ApiEnvelope, ApiErrorDetails } from "../types/api.ts";

const API_BASE_URL = import.meta.env.VITE_API_BASE_URL ?? "/api/v1";

export class ApiError extends Error implements ApiErrorDetails {
  readonly status: number;
  readonly code: string;
  readonly requestId: string;

  constructor(details: ApiErrorDetails) {
    super(details.message);
    this.name = "ApiError";
    this.status = details.status;
    this.code = details.code;
    this.requestId = details.requestId;
  }
}

type RequestOptions = Omit<RequestInit, "body"> & { body?: unknown; accessToken?: string };

async function request<T>(path: string, options: RequestOptions = {}): Promise<T> {
  const headers = new Headers(options.headers);
  headers.set("Accept", "application/json");
  if (options.body !== undefined) headers.set("Content-Type", "application/json");
  if (options.accessToken) headers.set("Authorization", `Bearer ${options.accessToken}`);

  const response = await fetch(`${API_BASE_URL}${path}`, {
    ...options,
    headers,
    body: options.body === undefined ? undefined : JSON.stringify(options.body),
  });
  const envelope = (await response.json()) as ApiEnvelope<T>;
  if (!response.ok) {
    throw new ApiError({
      status: response.status,
      code: envelope.code ?? "UNKNOWN_ERROR",
      message: envelope.message ?? "请求失败",
      requestId: envelope.request_id ?? response.headers.get("X-Request-ID") ?? "",
    });
  }
  if (envelope.data === undefined) return undefined as T;
  return envelope.data;
}

export const apiClient = {
  get: <T>(path: string, accessToken?: string) => request<T>(path, { method: "GET", accessToken }),
  post: <T>(path: string, body?: unknown, accessToken?: string) =>
    request<T>(path, { method: "POST", body, accessToken }),
  patch: <T>(path: string, body?: unknown, accessToken?: string) =>
    request<T>(path, { method: "PATCH", body, accessToken }),
  delete: <T>(path: string, accessToken?: string) => request<T>(path, { method: "DELETE", accessToken }),
};

