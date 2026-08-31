// @vitest-environment node

import { describe, expect, it } from "vitest";
import { decodeAccessToken, useAuthStore } from "../stores/authStore";

function token(payload: object) {
  return `header.${btoa(JSON.stringify(payload)).replace(/=/g, "").replace(/\+/g, "-").replace(/\//g, "_")}.signature`;
}

describe("decodeAccessToken", () => {
  it("reads GoIM user claims", () => {
    expect(decodeAccessToken(token({ user_id: 42, username: "alice", exp: 1_900_000_000 }))).toEqual({ user_id: 42, username: "alice", exp: 1_900_000_000 });
  });

  it("returns null for malformed input", () => {
    expect(decodeAccessToken("not-a-token")).toBeNull();
  });
});

describe("refresh token rotation", () => {
  it("atomically replaces both tokens", () => {
    useAuthStore.getState().clearSession();
    useAuthStore.getState().rotateSession({
      access_token: token({ user_id: 7, username: "alice", exp: 1_900_000_000 }),
      refresh_token: "refresh-v2",
      expires_in: 7200,
    });
    const state = useAuthStore.getState();
    expect(state.accessToken).toContain("header.");
    expect(state.refreshToken).toBe("refresh-v2");
    expect(state.user).toMatchObject({ id: 7, username: "alice" });
  });
});
