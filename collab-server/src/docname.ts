// Collaborative document naming scheme.
//
// page:{book_id}:{page_id} — source kitab page draft (the main editor).
//
// The scheme is intentionally extensible: production-section:{project_id}:{heading_id}
// is reserved for the translation workspace but not implemented yet — parsing
// rejects it so a future rollout is an additive change here and in the Go
// internal API.
const PAGE_DOC_RE = /^page:(\d{1,9}):(\d{1,9})$/;

export interface PageDoc {
  kind: "page";
  bookId: number;
  pageId: number;
}

export function parseDocName(name: string): PageDoc | null {
  const match = PAGE_DOC_RE.exec(name);
  if (!match) {
    return null;
  }

  const bookId = Number(match[1]);
  const pageId = Number(match[2]);
  if (bookId <= 0 || pageId <= 0) {
    return null;
  }

  return { kind: "page", bookId, pageId };
}
