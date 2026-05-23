import http from "node:http";
import { readFile } from "node:fs/promises";
import { extname, join, normalize } from "node:path";
import { fileURLToPath } from "node:url";

const root = fileURLToPath(new URL(".", import.meta.url));
const port = Number.parseInt(process.env.PORT || "8090", 10);
const backendURL = new URL(process.env.BACKEND_URL || "http://127.0.0.1:8080");

const mimeTypes = {
  ".html": "text/html; charset=utf-8",
  ".css": "text/css; charset=utf-8",
  ".js": "text/javascript; charset=utf-8",
  ".json": "application/json; charset=utf-8",
  ".svg": "image/svg+xml",
};

const server = http.createServer(async (req, res) => {
  try {
    const requestURL = new URL(req.url || "/", `http://${req.headers.host || "127.0.0.1"}`);
    if (requestURL.pathname.startsWith("/api/")) {
      await proxyBackend(req, res, requestURL);
      return;
    }

    await serveStatic(res, requestURL.pathname);
  } catch (error) {
    res.writeHead(500, { "Content-Type": "text/plain; charset=utf-8" });
    res.end(error instanceof Error ? error.message : String(error));
  }
});

server.listen(port, "127.0.0.1", () => {
  console.log(`Surau web reader: http://127.0.0.1:${port}`);
  console.log(`Proxy backend: ${backendURL.origin}`);
});

async function proxyBackend(req, res, requestURL) {
  const target = new URL(requestURL.pathname.replace(/^\/api/, ""), backendURL);
  target.search = requestURL.search;

  const headers = { ...req.headers };
  delete headers.host;

  const backendResponse = await fetch(target, {
    method: req.method,
    headers,
    body: ["GET", "HEAD"].includes(req.method || "GET") ? undefined : req,
    duplex: "half",
  });

  res.writeHead(backendResponse.status, Object.fromEntries(backendResponse.headers));
  if (backendResponse.body) {
    for await (const chunk of backendResponse.body) {
      res.write(chunk);
    }
  }
  res.end();
}

async function serveStatic(res, pathname) {
  const cleanPath = pathname === "/" ? "/index.html" : pathname;
  const filePath = normalize(join(root, cleanPath));
  if (!filePath.startsWith(root)) {
    res.writeHead(403);
    res.end("Forbidden");
    return;
  }

  try {
    const body = await readFile(filePath);
    const contentType = mimeTypes[extname(filePath)] || "application/octet-stream";
    res.writeHead(200, { "Content-Type": contentType });
    res.end(body);
  } catch {
    res.writeHead(404, { "Content-Type": "text/plain; charset=utf-8" });
    res.end("Not found");
  }
}
