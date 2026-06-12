import { describe, expect, it } from "vitest";

import { parseDocName } from "../src/docname.js";

describe("parseDocName", () => {
  it("parses a valid page document name", () => {
    expect(parseDocName("page:797:1")).toEqual({ kind: "page", bookId: 797, pageId: 1 });
    expect(parseDocName("page:990001:120")).toEqual({
      kind: "page",
      bookId: 990001,
      pageId: 120,
    });
  });

  it.each([
    "page:0:1",
    "page:1:0",
    "page:-1:2",
    "page:1",
    "page:1:2:3",
    "page:abc:1",
    "page:1:1 ",
    " page:1:1",
    "production-section:abc:1",
    "book:1:1",
    "",
    "page:9999999999:1",
  ])("rejects %j", (name) => {
    expect(parseDocName(name)).toBeNull();
  });
});
