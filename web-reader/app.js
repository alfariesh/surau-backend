const apiBase = "/api";
const pageLimit = 120;
const requestTimeoutMs = 15000;
const feedbackClientIDKey = "surau.feedback.client_id";

const state = {
  lang: "id",
  mode: "toc",
  books: [],
  activeBook: null,
  toc: [],
  flatToc: [],
  pages: [],
  pageOffset: 0,
  pageTotal: 0,
  activeHeadingID: null,
  activePageID: null,
  activeTranslation: null,
  showArabic: true,
  showTranslation: true,
};

const el = {
  bookSearch: document.querySelector("#bookSearch"),
  refreshBooks: document.querySelector("#refreshBooks"),
  bookList: document.querySelector("#bookList"),
  tocPanel: document.querySelector("#tocPanel"),
  tocTree: document.querySelector("#tocTree"),
  tocCount: document.querySelector("#tocCount"),
  pagePanel: document.querySelector("#pagePanel"),
  pageList: document.querySelector("#pageList"),
  pageCount: document.querySelector("#pageCount"),
  prevPageChunk: document.querySelector("#prevPageChunk"),
  nextPageChunk: document.querySelector("#nextPageChunk"),
  emptyState: document.querySelector("#emptyState"),
  readerArticle: document.querySelector("#readerArticle"),
  bookMeta: document.querySelector("#bookMeta"),
  sectionTitle: document.querySelector("#sectionTitle"),
  breadcrumb: document.querySelector("#breadcrumb"),
  translationBadge: document.querySelector("#translationBadge"),
  toggleArabic: document.querySelector("#toggleArabic"),
  toggleTranslation: document.querySelector("#toggleTranslation"),
  prevSection: document.querySelector("#prevSection"),
  nextSection: document.querySelector("#nextSection"),
  translationPane: document.querySelector("#translationPane"),
  translationContent: document.querySelector("#translationContent"),
  feedbackPanel: document.querySelector("#feedbackPanel"),
  feedbackLike: document.querySelector("#feedbackLike"),
  feedbackDislike: document.querySelector("#feedbackDislike"),
  feedbackForm: document.querySelector("#feedbackForm"),
  feedbackReason: document.querySelector("#feedbackReason"),
  feedbackNote: document.querySelector("#feedbackNote"),
  feedbackSubmit: document.querySelector("#feedbackSubmit"),
  feedbackCancel: document.querySelector("#feedbackCancel"),
  arabicPane: document.querySelector("#arabicPane"),
  arabicContent: document.querySelector("#arabicContent"),
  childrenPane: document.querySelector("#childrenPane"),
  childrenLinks: document.querySelector("#childrenLinks"),
  toast: document.querySelector("#toast"),
};

init();

function init() {
  bindEvents();
  restoreHashState();
  loadBooks();
}

function bindEvents() {
  document.querySelectorAll(".mode-button").forEach((button) => {
    button.addEventListener("click", () => setMode(button.dataset.mode || "toc"));
  });

  document.querySelectorAll(".lang-button").forEach((button) => {
    button.addEventListener("click", () => {
      state.lang = button.dataset.lang || "id";
      document.querySelectorAll(".lang-button").forEach((item) => {
        item.classList.toggle("is-active", item === button);
      });
      loadBooks({ keepActiveBook: true });
    });
  });

  el.bookSearch.addEventListener("input", debounce(() => loadBooks(), 260));
  el.refreshBooks.addEventListener("click", () => loadBooks({ keepActiveBook: true }));
  el.toggleArabic.addEventListener("click", () => {
    state.showArabic = !state.showArabic;
    applyPaneVisibility();
  });
  el.toggleTranslation.addEventListener("click", () => {
    state.showTranslation = !state.showTranslation;
    applyPaneVisibility();
  });
  el.prevSection.addEventListener("click", () => {
    if (state.mode === "page") {
      previousPage();
      return;
    }

    const current = activeTocIndex();
    if (current > 0) readHeading(state.flatToc[current - 1].heading_id);
  });
  el.nextSection.addEventListener("click", () => {
    if (state.mode === "page") {
      nextPage();
      return;
    }

    const current = activeTocIndex();
    if (current >= 0 && current + 1 < state.flatToc.length) readHeading(state.flatToc[current + 1].heading_id);
  });
  el.prevPageChunk.addEventListener("click", () => loadPageChunk(Math.max(0, state.pageOffset - pageLimit), { readLast: true }));
  el.nextPageChunk.addEventListener("click", () => loadPageChunk(state.pageOffset + pageLimit, { readFirst: true }));
  el.feedbackLike.addEventListener("click", () => submitFeedback("like"));
  el.feedbackDislike.addEventListener("click", () => {
    el.feedbackForm.classList.toggle("is-hidden");
    if (!el.feedbackForm.classList.contains("is-hidden")) el.feedbackNote.focus();
  });
  el.feedbackSubmit.addEventListener("click", () => submitFeedback("dislike"));
  el.feedbackCancel.addEventListener("click", () => {
    el.feedbackForm.classList.add("is-hidden");
    el.feedbackNote.value = "";
  });
  window.addEventListener("hashchange", handleHashChange);
}

async function loadBooks(options = {}) {
  try {
    setLoading(el.bookList, "Loading books...");
    const query = new URLSearchParams({
      lang: state.lang,
      has_content: "true",
      limit: "40",
      offset: "0",
    });
    const search = el.bookSearch.value.trim();
    if (search) query.set("q", search);

    const payload = await requestJSON(`/v1/books?${query}`);
    state.books = payload.books || [];
    renderBooks();

    const hashBookID = getHashInt("book");
    const nextBook =
      state.books.find((book) => book.id === hashBookID) ||
      (options.keepActiveBook && state.activeBook && state.books.find((book) => book.id === state.activeBook.id)) ||
      state.books[0];
    if (nextBook) {
      await selectBook(nextBook.id, { headingID: getHashInt("heading"), pageID: getHashInt("page") });
    } else {
      setLoading(el.tocTree, "No books found.");
      setLoading(el.pageList, "No books found.");
    }
  } catch (error) {
    showError(error);
    setLoading(el.bookList, "Backend tidak bisa diakses.");
    setLoading(el.tocTree, "Backend tidak bisa diakses.");
    setLoading(el.pageList, "Backend tidak bisa diakses.");
  }
}

function renderBooks() {
  el.bookList.innerHTML = "";
  if (!state.books.length) {
    setLoading(el.bookList, "No published books found.");
    return;
  }

  for (const book of state.books) {
    const button = document.createElement("button");
    button.type = "button";
    button.className = "book-item";
    button.classList.toggle("is-active", state.activeBook?.id === book.id);
    button.innerHTML = `
      <span class="book-title">${escapeHTML(book.name || `Book ${book.id}`)}</span>
      <span class="book-subtitle">${escapeHTML(book.author_name || "Unknown author")}</span>
      <span class="book-subtitle">${escapeHTML(book.category_name || "")}</span>
    `;
    button.addEventListener("click", () => selectBook(book.id));
    el.bookList.appendChild(button);
  }
}

function setMode(mode, options = {}) {
  state.mode = mode === "page" ? "page" : "toc";
  document.querySelectorAll(".mode-button").forEach((button) => {
    button.classList.toggle("is-active", button.dataset.mode === state.mode);
  });
  applyModeVisibility();

  if (options.skipRead || !state.activeBook) return;
  if (state.mode === "page") {
    readPage(state.activePageID || state.pages[0]?.page_id);
    return;
  }
  readHeading(state.activeHeadingID || state.flatToc[0]?.heading_id);
}

function applyModeVisibility() {
  el.tocPanel.classList.toggle("is-hidden", state.mode !== "toc");
  el.pagePanel.classList.toggle("is-hidden", state.mode !== "page");
}

async function selectBook(bookID, options = {}) {
  try {
    const book = await requestJSON(`/v1/books/${bookID}?${new URLSearchParams({ lang: state.lang })}`);
    state.activeBook = book;
    state.activeHeadingID = null;
    state.activePageID = null;
    renderBooks();
    await loadTOC(book.id);
    await loadPageChunk(0, { deferRead: true });
    applyModeVisibility();

    if (state.mode === "page") {
      await readPage(options.pageID || state.pages[0]?.page_id);
      return;
    }

    const headingID = options.headingID && state.flatToc.some((item) => item.heading_id === options.headingID)
      ? options.headingID
      : state.flatToc[0]?.heading_id;
    if (headingID) {
      await readHeading(headingID);
    }
  } catch (error) {
    showError(error);
  }
}

async function loadTOC(bookID) {
  setLoading(el.tocTree, "Loading TOC...");
  const query = new URLSearchParams({ lang: state.lang, include_audio: "true" });
  state.toc = await requestJSON(`/v1/books/${bookID}/toc?${query}`);
  state.flatToc = flattenTOC(state.toc);
  el.tocCount.textContent = `${state.flatToc.length} items`;
  renderTOC();
}

async function loadPageChunk(offset, options = {}) {
  if (!state.activeBook) return;
  const safeOffset = Math.max(0, offset);
  setLoading(el.pageList, "Loading pages...");
  const query = new URLSearchParams({ limit: String(pageLimit), offset: String(safeOffset) });
  const payload = await requestJSON(`/v1/books/${state.activeBook.id}/pages?${query}`);
  state.pages = payload.pages || [];
  state.pageTotal = payload.total || 0;
  state.pageOffset = safeOffset;
  renderPages();

  if (options.deferRead || !state.pages.length) return;
  if (options.readLast) {
    await readPage(state.pages[state.pages.length - 1].page_id);
    return;
  }
  if (options.readFirst) await readPage(state.pages[0].page_id);
}

function renderPages() {
  el.pageList.innerHTML = "";
  el.pageCount.textContent = state.pageTotal ? `${state.pageOffset + 1}-${state.pageOffset + state.pages.length} / ${state.pageTotal}` : "";
  el.prevPageChunk.disabled = state.pageOffset <= 0;
  el.nextPageChunk.disabled = state.pageOffset + state.pages.length >= state.pageTotal;

  if (!state.pages.length) {
    setLoading(el.pageList, "No pages found.");
    return;
  }

  for (const page of state.pages) {
    const button = document.createElement("button");
    button.type = "button";
    button.className = "page-item";
    button.classList.toggle("is-active", state.activePageID === page.page_id);
    button.innerHTML = `
      <span class="page-number">${escapeHTML(page.printed_page || page.page_id)}</span>
      <span class="page-meta">${escapeHTML(page.part ? `Jilid ${page.part}` : `ID ${page.page_id}`)}</span>
    `;
    button.addEventListener("click", () => readPage(page.page_id));
    el.pageList.appendChild(button);
  }
}

function renderTOC() {
  el.tocTree.innerHTML = "";
  if (!state.toc.length) {
    setLoading(el.tocTree, "TOC empty.");
    return;
  }
  el.tocTree.appendChild(renderTOCList(state.toc));
}

function renderTOCList(nodes) {
  const list = document.createElement("ul");
  list.className = "toc-list";
  for (const node of nodes) {
    const li = document.createElement("li");
    const button = document.createElement("button");
    button.type = "button";
    button.className = "toc-item";
    button.classList.toggle("is-active", state.activeHeadingID === node.heading_id);
    button.innerHTML = `
      <span class="toc-title">${escapeHTML(node.title || `Heading ${node.heading_id}`)}</span>
      <span class="toc-flags">
        <span class="mini-dot ${node.has_translation ? "has-translation" : ""}" title="Translation"></span>
        <span class="mini-dot ${node.has_audio ? "has-audio" : ""}" title="Audio"></span>
      </span>
    `;
    button.addEventListener("click", () => readHeading(node.heading_id));
    li.appendChild(button);
    if (node.children?.length) li.appendChild(renderTOCList(node.children));
    list.appendChild(li);
  }
  return list;
}

async function readHeading(headingID) {
  if (!state.activeBook) return;
  try {
    setMode("toc", { skipRead: true });
    state.activeHeadingID = headingID;
    renderTOC();
    const query = new URLSearchParams({ lang: state.lang });
    const section = await requestJSON(`/v1/books/${state.activeBook.id}/toc/${headingID}/read?${query}`);
    renderSection(section);
    updateHash();
  } catch (error) {
    showError(error);
  }
}

async function readPage(pageID) {
  if (!state.activeBook || !pageID) return;
  try {
    setMode("page", { skipRead: true });
    state.activePageID = pageID;
    renderPages();
    const page = await requestJSON(`/v1/books/${state.activeBook.id}/pages/${pageID}`);
    renderPage(page);
    updateHash();
  } catch (error) {
    showError(error);
  }
}

function renderSection(section) {
  el.emptyState.classList.add("is-hidden");
  el.readerArticle.classList.remove("is-hidden");

  el.bookMeta.textContent = [
    state.activeBook?.name,
    pageRange(section.start_page_id, section.end_page_id),
  ].filter(Boolean).join(" · ");
  el.sectionTitle.textContent = section.title || section.heading?.content || `Heading ${section.heading_id}`;

  el.breadcrumb.innerHTML = "";
  for (const crumb of section.breadcrumb || []) {
    const span = document.createElement("span");
    span.textContent = crumb.title;
    el.breadcrumb.appendChild(span);
  }

  renderTranslation(section.translation);
  el.arabicContent.innerHTML = section.original_html || "";
  renderChildren(section.children || []);
  renderNavigation();
  applyPaneVisibility();
}

function renderPage(page) {
  el.emptyState.classList.add("is-hidden");
  el.readerArticle.classList.remove("is-hidden");

  el.bookMeta.textContent = [
    state.activeBook?.name,
    page.part ? `jilid ${page.part}` : "",
    page.printed_page ? `halaman cetak ${page.printed_page}` : `page id ${page.page_id}`,
  ].filter(Boolean).join(" · ");
  el.sectionTitle.textContent = page.printed_page ? `Halaman ${page.printed_page}` : `Page ${page.page_id}`;
  el.breadcrumb.innerHTML = "";
  el.translationBadge.classList.remove("is-reviewed");
  el.translationBadge.textContent = "Page reader";
  el.translationContent.innerHTML = `<p class="muted">Mode halaman menampilkan teks Arab per halaman. Translation tetap tersedia di mode Bab.</p>`;
  state.activeTranslation = null;
  renderFeedbackPanel(null);
  el.arabicContent.innerHTML = page.content_html || "";
  el.childrenPane.classList.add("is-hidden");
  renderNavigation();
  applyPaneVisibility();
}

function renderTranslation(translation) {
  state.activeTranslation = translation || null;
  if (!translation) {
    el.translationContent.innerHTML = `<p class="muted">Translation belum diimport untuk bahasa ini.</p>`;
    renderFeedbackPanel(null);
    setBadge(null);
    return;
  }

  el.translationContent.innerHTML = renderMarkdown(translation.content || "");
  renderFeedbackPanel(translation);
  setBadge(translation);
}

function setBadge(translation) {
  el.translationBadge.classList.remove("is-reviewed");
  if (!translation) {
    el.translationBadge.textContent = "No translation";
    return;
  }

  if (translation.translation_status === "reviewed") {
    el.translationBadge.classList.add("is-reviewed");
    el.translationBadge.textContent = `Reviewed by ${translation.translation_reviewed_by || "editor"}`;
    return;
  }

  el.translationBadge.textContent = "Generated translation";
}

function renderChildren(children) {
  el.childrenLinks.innerHTML = "";
  el.childrenPane.classList.toggle("is-hidden", !children.length);
  for (const child of children) {
    const button = document.createElement("button");
    button.type = "button";
    button.className = "child-link";
    button.textContent = child.title;
    button.addEventListener("click", () => readHeading(child.heading_id));
    el.childrenLinks.appendChild(button);
  }
}

function renderNavigation() {
  if (state.mode === "page") {
    renderPageNavigation();
    return;
  }

  const index = activeTocIndex();
  const previous = index > 0 ? state.flatToc[index - 1] : null;
  const next = index >= 0 && index + 1 < state.flatToc.length ? state.flatToc[index + 1] : null;

  el.prevSection.disabled = !previous;
  el.nextSection.disabled = !next;
  el.prevSection.textContent = previous ? `← ${clip(previous.title, 34)}` : "← Previous";
  el.nextSection.textContent = next ? `${clip(next.title, 34)} →` : "Next →";
}

function renderFeedbackPanel(translation) {
  const canSendFeedback = state.mode === "toc" && translation && translation.lang && translation.lang !== "ar";
  el.feedbackPanel.classList.toggle("is-hidden", !canSendFeedback);
  el.feedbackForm.classList.add("is-hidden");
  el.feedbackNote.value = "";
  el.feedbackLike.disabled = !canSendFeedback;
  el.feedbackDislike.disabled = !canSendFeedback;
  el.feedbackSubmit.disabled = !canSendFeedback;
}

async function submitFeedback(vote) {
  if (!state.activeBook || !state.activeHeadingID || !state.activeTranslation) return;

  const payload = {
    vote,
    client_id: getFeedbackClientID(),
  };
  if (vote === "dislike") {
    payload.reason = el.feedbackReason.value;
    payload.note = el.feedbackNote.value.trim();
  }

  try {
    setFeedbackBusy(true);
    const query = new URLSearchParams({ lang: state.activeTranslation.lang || state.lang });
    await requestJSON(`/v1/books/${state.activeBook.id}/toc/${state.activeHeadingID}/translation-feedback?${query}`, {
      method: "POST",
      body: JSON.stringify(payload),
      headers: { "Content-Type": "application/json" },
    });
    showError(vote === "like" ? "Feedback saved: like" : "Feedback saved: dislike");
    el.feedbackForm.classList.add("is-hidden");
    el.feedbackNote.value = "";
  } catch (error) {
    showError(error);
  } finally {
    setFeedbackBusy(false);
  }
}

function setFeedbackBusy(isBusy) {
  el.feedbackLike.disabled = isBusy;
  el.feedbackDislike.disabled = isBusy;
  el.feedbackSubmit.disabled = isBusy;
  el.feedbackCancel.disabled = isBusy;
}

function renderPageNavigation() {
  const index = activePageIndex();
  const previous = index > 0 || state.pageOffset > 0;
  const next = (index >= 0 && index + 1 < state.pages.length) || state.pageOffset + state.pages.length < state.pageTotal;

  el.prevSection.disabled = !previous;
  el.nextSection.disabled = !next;
  el.prevSection.textContent = "← Previous page";
  el.nextSection.textContent = "Next page →";
}

async function previousPage() {
  const index = activePageIndex();
  if (index > 0) {
    await readPage(state.pages[index - 1].page_id);
    return;
  }
  if (state.pageOffset > 0) await loadPageChunk(Math.max(0, state.pageOffset - pageLimit), { readLast: true });
}

async function nextPage() {
  const index = activePageIndex();
  if (index >= 0 && index + 1 < state.pages.length) {
    await readPage(state.pages[index + 1].page_id);
    return;
  }
  if (state.pageOffset + state.pages.length < state.pageTotal) await loadPageChunk(state.pageOffset + pageLimit, { readFirst: true });
}

function applyPaneVisibility() {
  el.arabicPane.classList.toggle("is-hidden", !state.showArabic);
  el.translationPane.classList.toggle("is-hidden", state.mode === "page" || !state.showTranslation);
  el.toggleArabic.classList.toggle("is-muted", !state.showArabic);
  el.toggleTranslation.classList.toggle("is-muted", !state.showTranslation);
  el.toggleTranslation.disabled = state.mode === "page";
}

function renderMarkdown(markdown) {
  const lines = String(markdown || "").replace(/\r\n?/g, "\n").split("\n");
  const blocks = [];
  let paragraph = [];
  let quote = [];

  function flushParagraph() {
    if (!paragraph.length) return;
    blocks.push(`<p>${inlineMarkdown(paragraph.join(" "))}</p>`);
    paragraph = [];
  }

  function flushQuote() {
    if (!quote.length) return;
    blocks.push(`<blockquote>${quote.map((line) => `<p>${inlineMarkdown(line)}</p>`).join("")}</blockquote>`);
    quote = [];
  }

  for (const rawLine of lines) {
    const line = rawLine.trim();
    if (!line) {
      flushParagraph();
      flushQuote();
      continue;
    }
    if (line.startsWith(">")) {
      flushParagraph();
      quote.push(line.replace(/^>\s?/, ""));
      continue;
    }
    flushQuote();
    if (/^#{2,3}\s+/.test(line)) {
      flushParagraph();
      const level = line.startsWith("###") ? "h3" : "h2";
      blocks.push(`<${level}>${inlineMarkdown(line.replace(/^#{2,3}\s+/, ""))}</${level}>`);
      continue;
    }
    paragraph.push(line);
  }

  flushParagraph();
  flushQuote();
  return blocks.join("");
}

function inlineMarkdown(value) {
  return escapeHTML(value)
    .replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>")
    .replace(/\*([^*]+)\*/g, "<em>$1</em>")
    .replace(/\[(\d{1,3})\]/g, "<sup>[$1]</sup>");
}

async function requestJSON(path, options = {}) {
  const controller = new AbortController();
  const timeout = window.setTimeout(() => controller.abort(), requestTimeoutMs);

  try {
    const response = await fetch(`${apiBase}${path}`, {
      method: options.method || "GET",
      headers: { Accept: "application/json", ...(options.headers || {}) },
      body: options.body,
      signal: controller.signal,
    });
    if (!response.ok) {
      const text = await response.text();
      throw new Error(`${response.status} ${response.statusText}: ${text}`);
    }
    return response.json();
  } catch (error) {
    if (error.name === "AbortError") {
      throw new Error("Request timeout. Backend atau database belum siap.");
    }
    throw error;
  } finally {
    window.clearTimeout(timeout);
  }
}

function getFeedbackClientID() {
  let clientID = window.localStorage.getItem(feedbackClientIDKey);
  if (clientID) return clientID;

  clientID = window.crypto?.randomUUID ? window.crypto.randomUUID() : `client-${Date.now()}-${Math.random().toString(16).slice(2)}`;
  window.localStorage.setItem(feedbackClientIDKey, clientID);
  return clientID;
}

function flattenTOC(nodes) {
  const result = [];
  function visit(items) {
    for (const item of items || []) {
      result.push(item);
      visit(item.children);
    }
  }
  visit(nodes);
  return result;
}

function activeTocIndex() {
  return state.flatToc.findIndex((item) => item.heading_id === state.activeHeadingID);
}

function activePageIndex() {
  return state.pages.findIndex((item) => item.page_id === state.activePageID);
}

function restoreHashState() {
  const mode = getHashValue("mode");
  if (mode) state.mode = mode === "page" ? "page" : "toc";

  const lang = getHashValue("lang");
  if (lang && ["id", "en", "ar"].includes(lang)) {
    state.lang = lang;
    document.querySelectorAll(".lang-button").forEach((button) => {
      button.classList.toggle("is-active", button.dataset.lang === lang);
    });
  }
  document.querySelectorAll(".mode-button").forEach((button) => {
    button.classList.toggle("is-active", button.dataset.mode === state.mode);
  });
  applyModeVisibility();
}

function handleHashChange() {
  restoreHashState();

  const bookID = getHashInt("book");
  if (!bookID) return;

  const options = { headingID: getHashInt("heading"), pageID: getHashInt("page") };
  if (!state.activeBook || state.activeBook.id !== bookID) {
    selectBook(bookID, options);
    return;
  }

  if (state.mode === "page" && options.pageID && options.pageID !== state.activePageID) {
    readPage(options.pageID);
    return;
  }

  if (state.mode === "toc" && options.headingID && options.headingID !== state.activeHeadingID) {
    readHeading(options.headingID);
  }
}

function updateHash() {
  if (!state.activeBook) return;
  const params = new URLSearchParams({
    book: String(state.activeBook.id),
    mode: state.mode,
    lang: state.lang,
  });
  if (state.mode === "page" && state.activePageID) {
    params.set("page", String(state.activePageID));
  } else if (state.activeHeadingID) {
    params.set("heading", String(state.activeHeadingID));
  }
  history.replaceState(null, "", `#${params}`);
}

function getHashInt(key) {
  const value = getHashValue(key);
  const parsed = Number.parseInt(value || "", 10);
  return Number.isFinite(parsed) && parsed > 0 ? parsed : null;
}

function getHashValue(key) {
  const raw = window.location.hash.replace(/^#/, "");
  return new URLSearchParams(raw).get(key);
}

function setLoading(container, message) {
  container.innerHTML = `<p class="muted">${escapeHTML(message)}</p>`;
}

function showError(error) {
  el.toast.textContent = error.message || String(error);
  el.toast.classList.add("is-visible");
  window.clearTimeout(showError.timer);
  showError.timer = window.setTimeout(() => el.toast.classList.remove("is-visible"), 3800);
}

function pageRange(start, end) {
  if (!start || !end) return "";
  return start === end ? `page ${start}` : `pages ${start}-${end}`;
}

function escapeHTML(value) {
  return String(value ?? "")
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#039;");
}

function clip(value, length) {
  const text = String(value || "");
  return text.length > length ? `${text.slice(0, length - 1)}…` : text;
}

function debounce(fn, wait) {
  let timer;
  return (...args) => {
    window.clearTimeout(timer);
    timer = window.setTimeout(() => fn(...args), wait);
  };
}
