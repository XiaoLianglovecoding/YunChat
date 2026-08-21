import { describe, expect, it } from "vitest";

import { relativeTime } from "./date.ts";

describe("relativeTime", () => {
  it("formats recent minutes", () => {
    const now = new Date("2026-08-21T08:10:00Z");
    expect(relativeTime("2026-08-21T08:05:00Z", now)).toBe("5分钟前");
  });
});

