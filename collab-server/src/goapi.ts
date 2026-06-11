// Client for the Go backend's internal collab API (service-token guarded).
// All draft reads/writes flow through Go so sanitization, content_text
// extraction, audit logs and revision history stay on the single write path —
// this process never touches the editorial tables directly.
import type { Logger } from "./logger.js";

export interface GoApiOptions {
  baseUrl: string;
  serviceToken: string;
  logger: Logger;
}

export interface PageDraft {
  book_id: number;
  page_id: number;
  source: "draft" | "raw";
  content_html: string;
  updated_at: string;
}

const RETRY_DELAYS_MS = [500, 2000, 5000];

export class GoApi {
  constructor(private readonly opts: GoApiOptions) {}

  async fetchPageDraft(bookId: number, pageId: number): Promise<PageDraft> {
    const response = await fetch(
      `${this.opts.baseUrl}/internal/collab/books/${bookId}/pages/${pageId}/draft`,
      {
        headers: { "X-Internal-Token": this.opts.serviceToken },
        signal: AbortSignal.timeout(10000),
      },
    );
    if (!response.ok) {
      throw new Error(`fetch page draft ${bookId}:${pageId} returned ${response.status}`);
    }

    return (await response.json()) as PageDraft;
  }

  // putPageDraft retries on transient failures; the caller persists the Yjs
  // binary regardless, so a failed sync here never loses data — the next
  // debounced store retries with the latest document.
  async putPageDraft(
    bookId: number,
    pageId: number,
    contentHtml: string,
    actorId: string,
    contributors: string[],
  ): Promise<void> {
    let lastError: unknown;

    for (let attempt = 0; attempt <= RETRY_DELAYS_MS.length; attempt++) {
      try {
        const response = await fetch(
          `${this.opts.baseUrl}/internal/collab/books/${bookId}/pages/${pageId}/draft`,
          {
            method: "PUT",
            headers: {
              "Content-Type": "application/json",
              "X-Internal-Token": this.opts.serviceToken,
            },
            body: JSON.stringify({
              content_html: contentHtml,
              actor_id: actorId,
              contributors,
            }),
            signal: AbortSignal.timeout(10000),
          },
        );

        if (response.ok) {
          return;
        }
        // 4xx other than 429 will not succeed on retry.
        if (response.status >= 400 && response.status < 500 && response.status !== 429) {
          throw new Error(`put page draft returned ${response.status}`);
        }

        lastError = new Error(`put page draft returned ${response.status}`);
      } catch (err) {
        lastError = err;
      }

      if (attempt < RETRY_DELAYS_MS.length) {
        this.opts.logger.warn(
          { bookId, pageId, attempt, err: String(lastError) },
          "put page draft failed, retrying",
        );
        await new Promise((resolve) => setTimeout(resolve, RETRY_DELAYS_MS[attempt]));
      }
    }

    throw lastError instanceof Error ? lastError : new Error(String(lastError));
  }
}
