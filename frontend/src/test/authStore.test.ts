// @vitest-environment node

import { describe, expect, it } from "vitest";
import { decodeAccessToken } from "../stores/authStore";

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
