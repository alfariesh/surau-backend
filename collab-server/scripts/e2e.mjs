// E2E verification: two collaborative clients on one kitab page document.
//
//   TOKEN=<editor access token> node scripts/e2e.mjs [ws://localhost:8090] [page:990001:1]
//
// Asserts: both clients sync the seeded content, an edit from client A
// converges on client B, and exits 0 — the caller then checks the Go draft
// row materialized after the debounce window.
import { HocuspocusProvider } from "@hocuspocus/provider";
import * as Y from "yjs";

const url = process.argv[2] ?? "ws://localhost:8090";
const docName = process.argv[3] ?? "page:990001:1";
const token = process.env.TOKEN;
if (!token) {
  console.error("TOKEN env var is required");
  process.exit(2);
}

const MARKER = `kolaborasi-${Date.now()}`;

function connect(label) {
  return new Promise((resolve, reject) => {
    const timeout = setTimeout(() => reject(new Error(`${label}: sync timeout`)), 15000);
    const provider = new HocuspocusProvider({
      url,
      name: docName,
      token,
      onSynced() {
        clearTimeout(timeout);
        resolve(provider);
      },
      onAuthenticationFailed({ reason }) {
        clearTimeout(timeout);
        reject(new Error(`${label}: authentication failed: ${reason}`));
      },
    });
  });
}

function fragmentText(provider) {
  return provider.document.getXmlFragment("default").toString();
}

async function waitFor(label, predicate, timeoutMs = 10000) {
  const start = Date.now();
  while (Date.now() - start < timeoutMs) {
    if (predicate()) {
      return;
    }
    await new Promise((resolve) => setTimeout(resolve, 200));
  }
  throw new Error(`${label}: condition not met within ${timeoutMs}ms`);
}

const a = await connect("clientA");
console.log("clientA synced");
await waitFor("clientA seeded", () => fragmentText(a).includes("المسألة"));
console.log("clientA sees seeded content");

const b = await connect("clientB");
await waitFor("clientB seeded", () => fragmentText(b).includes("المسألة"));
console.log("clientB sees seeded content");

// Client A appends a schema-valid paragraph.
const paragraph = new Y.XmlElement("paragraph");
paragraph.insert(0, [new Y.XmlText(MARKER)]);
a.document.getXmlFragment("default").push([paragraph]);
console.log("clientA inserted", MARKER);

await waitFor("clientB convergence", () => fragmentText(b).includes(MARKER));
console.log("clientB converged");

// Give the server one debounce window to flush HTML into the Go draft.
await new Promise((resolve) => setTimeout(resolve, 5000));

a.destroy();
b.destroy();
console.log(`MARKER=${MARKER}`);
process.exit(0);
