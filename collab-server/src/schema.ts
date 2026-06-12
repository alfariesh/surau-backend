// TipTap/ProseMirror schema mirroring the Go sanitizer allowlist
// (internal/readerutil/content.go). Anything the schema cannot represent is
// silently dropped during HTML -> Y.Doc seeding, which is data loss — so every
// allowlisted tag needs a node/mark here, and the seven global attributes ride
// along on all of them.
//
// Allowlist: a b blockquote br cite code dd div dl dt em h1..h6 hr i li ol p
// pre small span section strong sub sup table tbody td tfoot th thead tr u ul
// Global attrs: class data-marker data-type dir id lang title; a also: href name
//
// Known, accepted round-trip normalizations (verified by verifyRoundTrip and
// the fixture tests; the reverse direction is always re-sanitized in Go):
//   - <b> renders as <strong>, <i> as <em> (semantic equivalents)
//   - bare text directly inside <div>/<section>/<blockquote>/<dd> gains a <p>
//     wrapper (ProseMirror block model); stable after the first pass
//   - <thead>/<tfoot> wrappers flatten into <tbody> (rows and <th> survive)
import { Extension, Mark, Node } from "@tiptap/core";
import Link from "@tiptap/extension-link";
import Subscript from "@tiptap/extension-subscript";
import Superscript from "@tiptap/extension-superscript";
import { Table, TableCell, TableHeader, TableRow } from "@tiptap/extension-table";
import StarterKit from "@tiptap/starter-kit";

const GLOBAL_ATTRIBUTES = ["class", "data-marker", "data-type", "dir", "id", "lang", "title"] as const;

// Generic block container: used for div and section, which the source corpus
// uses for layout and footnote sections (section[data-type=footnotes]).
function blockContainer(name: string, tag: string, priority: number) {
  return Node.create({
    name,
    group: "block",
    content: "block+",
    defining: true,
    priority,
    parseHTML() {
      return [{ tag }];
    },
    renderHTML({ HTMLAttributes }) {
      return [tag, HTMLAttributes, 0];
    },
  });
}

export const Div = blockContainer("div", "div", 50);
export const Section = blockContainer("section", "section", 50);

export const DefinitionList = Node.create({
  name: "definitionList",
  group: "block",
  content: "(definitionTerm | definitionDescription)+",
  parseHTML() {
    return [{ tag: "dl" }];
  },
  renderHTML({ HTMLAttributes }) {
    return ["dl", HTMLAttributes, 0];
  },
});

export const DefinitionTerm = Node.create({
  name: "definitionTerm",
  content: "inline*",
  defining: true,
  parseHTML() {
    return [{ tag: "dt" }];
  },
  renderHTML({ HTMLAttributes }) {
    return ["dt", HTMLAttributes, 0];
  },
});

export const DefinitionDescription = Node.create({
  name: "definitionDescription",
  content: "block+",
  defining: true,
  parseHTML() {
    return [{ tag: "dd" }];
  },
  renderHTML({ HTMLAttributes }) {
    return ["dd", HTMLAttributes, 0];
  },
});

function simpleMark(name: string, tag: string) {
  return Mark.create({
    name,
    parseHTML() {
      return [{ tag }];
    },
    renderHTML({ HTMLAttributes }) {
      return [tag, HTMLAttributes, 0];
    },
  });
}

export const Small = simpleMark("small", "small");
export const Cite = simpleMark("cite", "cite");

// Span carries the footnote markers (span[data-type=footnote-marker]) and
// other annotated inline runs. Without attributes a span is meaningless, but
// it is kept for fidelity; the Go sanitizer tolerates it either way.
export const SpanMark = Mark.create({
  name: "spanMark",
  priority: 50,
  parseHTML() {
    return [{ tag: "span" }];
  },
  renderHTML({ HTMLAttributes }) {
    return ["span", HTMLAttributes, 0];
  },
});

// TipTap v3's table family injects markup the Go sanitizer strips anyway:
// style="min-width" + <colgroup>/<col> on <table>, and explicit colspan="1"/
// rowspan="1" on every cell. Rendering them would break the round-trip
// fixpoint (emit -> sanitize-strip -> differ). These overrides render the
// clean sanitized shape directly; parsing still captures colspan/rowspan > 1
// into the document model.
const SurauTable = Table.extend({
  renderHTML({ HTMLAttributes }) {
    return ["table", HTMLAttributes, ["tbody", 0]];
  },
});

function cleanCellRenderHTML(tag: "td" | "th") {
  return function renderHTML({ HTMLAttributes }: { HTMLAttributes: Record<string, unknown> }) {
    const { colspan, rowspan, colwidth: _colwidth, style: _style, ...rest } = HTMLAttributes;
    const attrs: Record<string, unknown> = { ...rest };
    if (colspan != null && String(colspan) !== "1") {
      attrs.colspan = colspan;
    }
    if (rowspan != null && String(rowspan) !== "1") {
      attrs.rowspan = rowspan;
    }

    return [tag, attrs, 0] as const;
  };
}

const SurauTableCell = TableCell.extend({
  renderHTML: cleanCellRenderHTML("td"),
});

const SurauTableHeader = TableHeader.extend({
  renderHTML: cleanCellRenderHTML("th"),
});

// The sanitizer allows a[href, name]; name is used by in-text anchors.
const SurauLink = Link.extend({
  addAttributes() {
    return {
      ...this.parent?.(),
      name: {
        default: null,
        parseHTML: (element) => element.getAttribute("name"),
        renderHTML: (attributes) => (attributes.name ? { name: attributes.name } : {}),
      },
    };
  },
}).configure({
  openOnClick: false,
  autolink: false,
  linkOnPaste: false,
  // The Go sanitizer strips rel/target anyway; do not inject them.
  HTMLAttributes: { rel: null, target: null },
});

// Rides the seven sanitizer-allowed global attributes on every node and mark
// so they survive HTML -> Y.Doc -> HTML round trips.
const GlobalAttributes = Extension.create({
  name: "surauGlobalAttributes",
  addGlobalAttributes() {
    return [
      {
        types: [
          "paragraph",
          "heading",
          "blockquote",
          "bulletList",
          "orderedList",
          "listItem",
          "codeBlock",
          "horizontalRule",
          "hardBreak",
          "table",
          "tableRow",
          "tableCell",
          "tableHeader",
          "div",
          "section",
          "definitionList",
          "definitionTerm",
          "definitionDescription",
          "bold",
          "italic",
          "underline",
          "subscript",
          "superscript",
          "code",
          "link",
          "small",
          "cite",
          "spanMark",
        ],
        attributes: Object.fromEntries(
          GLOBAL_ATTRIBUTES.map((attr) => [
            attr,
            {
              default: null,
              parseHTML: (element: HTMLElement) => element.getAttribute(attr),
              renderHTML: (attributes: Record<string, unknown>) =>
                attributes[attr] != null ? { [attr]: attributes[attr] } : {},
            },
          ]),
        ),
      },
    ];
  },
});

// The complete server-side schema. The frontend editor must configure the
// exact same list (documented in docs/collab.md) — a schema mismatch between
// client and server corrupts the shared Y.Doc rendering.
export const surauExtensions = [
  StarterKit.configure({
    // y-prosemirror supplies collaborative undo; ProseMirror history must be off.
    undoRedo: false,
    // <s> is not in the sanitizer allowlist; strike would be silently stripped.
    strike: false,
    // Replaced by SurauLink below (adds the a[name] attribute, drops rel/target).
    link: false,
  }),
  SurauLink,
  Subscript,
  Superscript,
  SurauTable.configure({ resizable: false }),
  TableRow,
  SurauTableHeader,
  SurauTableCell,
  Div,
  Section,
  DefinitionList,
  DefinitionTerm,
  DefinitionDescription,
  Small,
  Cite,
  SpanMark,
  GlobalAttributes,
];
