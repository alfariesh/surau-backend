// HTML <-> Y.Doc conversion through the shared TipTap schema.
//
// Seeding: sanitized draft HTML -> ProseMirror JSON -> Y.Doc (fragment
// "default", the TipTap Collaboration default). Persisting: Y.Doc -> JSON ->
// HTML, which then flows through the Go sanitizer again.
import { getSchema } from "@tiptap/core";
import { generateHTML, generateJSON } from "@tiptap/html";
import { prosemirrorJSONToYDoc, yDocToProsemirrorJSON } from "y-prosemirror";
import * as Y from "yjs";

import { surauExtensions } from "./schema.js";

export const Y_FRAGMENT_FIELD = "default";

const schema = getSchema(surauExtensions);

export function htmlToYDoc(html: string): Y.Doc {
  const json = generateJSON(html, surauExtensions);

  return prosemirrorJSONToYDoc(schema, json, Y_FRAGMENT_FIELD);
}

export function yDocToHtml(doc: Y.Doc): string {
  const json = yDocToProsemirrorJSON(doc, Y_FRAGMENT_FIELD);

  return generateHTML(json, surauExtensions);
}

export interface RoundTripReport {
  identical: boolean;
  original: string;
  roundTripped: string;
}

// verifyRoundTrip reports whether HTML survives the schema unchanged. Known
// normalizations (b -> strong, paragraph-wrapping inside div) make a diff
// expected for legacy content — the caller logs it; the pre-collab draft is
// already protected as a source-edit revision on the Go side.
export function verifyRoundTrip(html: string): RoundTripReport {
  const doc = htmlToYDoc(html);
  const roundTripped = yDocToHtml(doc);

  return {
    identical: normalizeForCompare(html) === normalizeForCompare(roundTripped),
    original: html,
    roundTripped,
  };
}

function normalizeForCompare(html: string): string {
  return html
    .replace(/>\s+</g, "><")
    .replace(/\s+/g, " ")
    .trim();
}
