import { describe, expect, it, vi } from "vitest";
// @vitest-environment node

import { ApiError, GoIMApiClient } from "../api/client";

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

describe("GoIMApiClient", () => {
  it("invokes the default fetch with the global object binding", async () => {
    const originalFetch = globalThis.fetch;
    const fetchMock = vi.fn(function (this: typeof globalThis) {
      if (this !== globalThis) throw new TypeError("Illegal invocation");
      return Promise.resolve(jsonResponse({ code: 0, message: "ok", data: "connected" }));
    });
    globalThis.fetch = fetchMock as typeof fetch;
    try {
      const client = new GoIMApiClient({ baseUrl: "http://example.test" });
      await expect(client.get<string>("/health")).resolves.toBe("connected");
      expect(fetchMock).toHaveBeenCalledTimes(1);
    } finally {
      globalThis.fetch = originalFetch;
    }
  });

  it("adds the access token and unwraps successful data", async () => {
    const fetchMock = vi.fn(async (_url: RequestInfo | URL, init?: RequestInit) => {
      expect(new Headers(init?.headers).get("Authorization")).toBe("Bearer access-token");
      return jsonResponse({ code: 0, message: "ok", data: { id: 7 } });
    });
    const client = new GoIMApiClient({ baseUrl: "http://example.test/api", getAccessToken: () => "access-token", fetch: fetchMock as typeof fetch });
    await expect(client.get<{ id: number }>("/item")).resolves.toEqual({ id: 7 });
  });

  it("refreshes once and retries an unauthorized request", async () => {
    let requestCount = 0;
    const recover = vi.fn(async () => true);
    const fetchMock = vi.fn(async () => {
      requestCount += 1;
      return requestCount === 1 ? jsonResponse({ code: 1003, message: "expired" }, 401) : jsonResponse({ code: 0, message: "ok", data: "done" });
    });
    const client = new GoIMApiClient({ baseUrl: "http://example.test", onUnauthorized: recover, fetch: fetchMock as typeof fetch });
    await expect(client.get<string>("/protected")).resolves.toBe("done");
    expect(recover).toHaveBeenCalledTimes(1);
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("throws the API error without an endless retry", async () => {
    const fetchMock = vi.fn(async () => jsonResponse({ code: 1106, message: "invalid token" }, 401));
    const client = new GoIMApiClient({ baseUrl: "http://example.test", onUnauthorized: async () => true, fetch: fetchMock as typeof fetch });
    await expect(client.get("/protected")).rejects.toMatchObject({ code: 1106, status: 401 });
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("normalizes network failures and request timeouts", async () => {
    const networkClient = new GoIMApiClient({ baseUrl: "http://example.test", fetch: vi.fn(async () => { throw new TypeError("offline"); }) as typeof fetch });
    await expect(networkClient.get("/health")).rejects.toMatchObject({ code: -1, status: 0, message: "网络连接失败，请检查后端服务" } satisfies Partial<ApiError>);

    vi.useFakeTimers();
    const timeoutClient = new GoIMApiClient({
      baseUrl: "http://example.test",
      timeoutMs: 5,
      fetch: vi.fn((_url: RequestInfo | URL, init?: RequestInit) => new Promise<Response>((_, reject) => init?.signal?.addEventListener("abort", () => reject(new DOMException("aborted", "AbortError"))))) as typeof fetch,
    });
    const assertion = expect(timeoutClient.get("/slow")).rejects.toMatchObject({ code: -1, status: 0, message: "请求超时，请稍后重试" } satisfies Partial<ApiError>);
    await vi.advanceTimersByTimeAsync(5);
    await assertion;
    vi.useRealTimers();
  });

  it("normalizes non-JSON and malformed JSON error responses", async () => {
    const responses = [
      new Response("upstream unavailable", { status: 502, statusText: "Bad Gateway", headers: { "Content-Type": "text/plain" } }),
      new Response("{", { status: 500, headers: { "Content-Type": "application/json" } }),
    ];
    const client = new GoIMApiClient({ baseUrl: "http://example.test", fetch: vi.fn(async () => responses.shift()!) as typeof fetch });
    await expect(client.get("/gateway")).rejects.toMatchObject({ code: -1, status: 502, message: "Bad Gateway" } satisfies Partial<ApiError>);
    await expect(client.get("/broken-json")).rejects.toMatchObject({ code: -1, status: 500, message: "请求失败，请稍后重试" } satisfies Partial<ApiError>);
  });
});
