// Fidelity tests for HTML <-> Y.Doc conversion against the canonical reader
// markup produced by the Go side (internal/readerutil/content.go). Anything
// the schema drops here is editor data loss, so these fixtures mirror real
// kitab pages: Arabic RTL text, footnote markers, Quran quotes, tables.
import { describe, expect, it } from "vitest";

import { htmlToYDoc, verifyRoundTrip, yDocToHtml } from "../src/convert.js";

function roundTrip(html: string): string {
  return yDocToHtml(htmlToYDoc(html));
}

describe("round-trip fidelity", () => {
  it("preserves a canonical reader page (headings, footnote refs, quran quote, footnotes section)", () => {
    const html = [
      `<h3 dir="rtl" lang="ar">المسألة الأولى</h3>`,
      `<p dir="rtl" lang="ar">نص أول <sup data-type="footnote-ref">(¬١)</sup></p>`,
      `<blockquote data-type="quran-quote" dir="rtl" lang="ar"><p>{قُلْ هُوَ اللَّهُ أَحَدٌ} [الإخلاص- ١]</p></blockquote>`,
      `<section data-type="footnotes" dir="rtl" lang="ar"><ol><li data-marker="(¬١)"><p><span data-type="footnote-marker">(¬١)</span> حاشية أولى<br>تتمة الحاشية</p></li></ol></section>`,
    ].join("");

    const result = roundTrip(html);

    // Text and semantics survive verbatim.
    expect(result).toContain("المسألة الأولى");
    expect(result).toContain(`<sup data-type="footnote-ref">(¬١)</sup>`);
    expect(result).toContain(`<span data-type="footnote-marker">(¬١)</span>`);
    expect(result).toContain("حاشية أولى<br>تتمة الحاشية");
    expect(result).toContain(`data-type="quran-quote"`);
    expect(result).toContain(`<section data-type="footnotes" dir="rtl" lang="ar">`);
    expect(result).toContain(`<li data-marker="(¬١)">`);
    expect(result).toContain(`dir="rtl"`);
    expect(result).toContain(`lang="ar"`);

    // Block-normalized markup is a stable fixpoint: a second pass is identical.
    expect(roundTrip(result)).toBe(result);
    expect(verifyRoundTrip(html).identical).toBe(true);
  });

  it("preserves span titles with ids (TOC anchors)", () => {
    const html = `<p><span data-type="title" id="toc-1">باب العلم</span></p>`;

    const result = roundTrip(html);

    expect(result).toContain(`data-type="title"`);
    expect(result).toContain(`id="toc-1"`);
    expect(result).toContain("باب العلم");
  });

  it("preserves tables with header cells", () => {
    const html =
      `<table><tbody><tr><th>عنوان</th><th>قيمة</th></tr>` +
      `<tr><td dir="rtl">أ</td><td>ب</td></tr></tbody></table>`;

    const result = roundTrip(html);

    expect(result).toContain("<table>");
    expect(result).toContain("<th");
    expect(result).toContain(`dir="rtl"`);
    expect(result).toContain("عنوان");
    expect(roundTrip(result)).toBe(result);
  });

  it("preserves links with href and name", () => {
    const html = `<p><a href="#fn-1" name="ref-1">المرجع</a></p>`;

    const result = roundTrip(html);

    expect(result).toContain(`href="#fn-1"`);
    expect(result).toContain(`name="ref-1"`);
    // The schema must not inject rel/target (the Go sanitizer would strip
    // them, churning every save).
    expect(result).not.toContain("rel=");
    expect(result).not.toContain("target=");
  });

  it("preserves nested lists, code, definition lists, and inline marks", () => {
    const html =
      `<ol><li><p>أول</p><ul><li><p>فرعي <code>نص</code></p></li></ul></li></ol>` +
      `<dl><dt>مصطلح</dt><dd><p>تعريف <small>صغير</small> <cite>مصدر</cite> <u>تحته خط</u> <sub>س</sub></p></dd></dl>`;

    const result = roundTrip(html);

    for (const fragment of [
      "<ol>", "<ul>", "<code>نص</code>", "<dl>", "<dt>مصطلح</dt>", "<dd>",
      "<small>صغير</small>", "<cite>مصدر</cite>", "<u>تحته خط</u>", "<sub>س</sub>",
    ]) {
      expect(result).toContain(fragment);
    }
    expect(roundTrip(result)).toBe(result);
  });

  it("normalizes legacy markup to stable semantic equivalents", () => {
    // <b>/<i> become <strong>/<em>; bare text in div gains a <p> wrapper.
    const html = `<div>نص <b>غامق</b> و<i>مائل</i></div>`;

    const result = roundTrip(html);

    expect(result).toContain("<strong>غامق</strong>");
    expect(result).toContain("<em>مائل</em>");
    expect(result).toContain("<div><p>");
    // Normalization converges: the second pass changes nothing.
    expect(roundTrip(result)).toBe(result);
  });

  it("drops markup outside the schema instead of corrupting the document", () => {
    const html = `<p>نص<iframe src="https://evil"></iframe></p>`;

    const result = roundTrip(html);

    expect(result).toContain("نص");
    expect(result).not.toContain("iframe");
  });

  it("handles the empty document", () => {
    const result = roundTrip("");

    expect(typeof result).toBe("string");
  });
});
