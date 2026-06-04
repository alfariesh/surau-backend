# Mobile Backend Integration Guide

Last updated: 2026-06-04

Dokumen ini adalah panduan utama untuk implementasi mobile app Islamic Surau dari backend ini. Fokusnya adalah kebutuhan FE mobile: urutan API call per screen, auth, data shape yang penting, strategi cache, error handling, dan behavior UI saat data terjemahan/audio belum lengkap.

Dokumen detail yang sudah ada dan tetap menjadi rujukan:

- `docs/auth-frontend.md` untuk auth, email verification, reset password, change email, dan delete account.
- `docs/user-onboarding-api.md` untuk profile, onboarding, preferences, dan user interests.
- `docs/frontend-integration-contract.md` untuk kontrak gabungan kitab + Quran.
- `docs/kitab-multilingual-api.md` dan `docs/kitab-frontend-contract.md` untuk kitab reader.
- `docs/quran-api.md` untuk Quran reader, audio, search, juz, hizb, dan ayah response.
- `/swagger/index.html` saat backend berjalan untuk OpenAPI generated reference.

## 1. Base Contract

Base URL disarankan lewat environment mobile:

```text
DEV_API_BASE_URL=http://localhost:8080
STAGING_API_BASE_URL=https://staging-api.example.com
PROD_API_BASE_URL=https://api.example.com
```

Semua REST endpoint utama memakai prefix:

```text
{API_BASE_URL}/v1
```

Header umum:

```http
Accept: application/json
Content-Type: application/json
```

Protected endpoint wajib memakai:

```http
Authorization: Bearer <token>
```

Health check untuk environment/debug:

| Method | Path | Auth | Kegunaan |
| --- | --- | --- | --- |
| `GET` | `/healthz` | Public | Server hidup. |
| `GET` | `/readyz` | Public | Server dan database siap. |
| `GET` | `/swagger/index.html` | Public jika enabled | Referensi OpenAPI. |

## 1.1 Jangan Terlewat Saat Implementasi FE

Hal-hal yang paling mudah membuat FE mobile salah kontrak:

- Tidak semua list response memakai wrapper `items`. Beberapa endpoint mengembalikan array langsung.
- Endpoint `DELETE /v1/me/saved-items/{id}` sukses dengan `204 No Content`, jadi client wrapper harus bisa menerima body kosong.
- `GET /v1/me/progress/{book_id}`, `GET /v1/me/quran/progress`, dan `GET /v1/me/quran/progress/surahs/{surah_id}` bisa `404` saat user belum punya progress. Treat sebagai "belum ada resume", bukan fatal error.
- Query boolean paling aman dikirim sebagai `true` atau `false`. Nilai non-boolean seperti `include_audio=yes` menghasilkan `400`.
- Query int opsional seperti `book_id`, `surah_id`, `from`, dan `to` harus positive integer.
- Path ID harus positive integer. `surah_id` valid `1..114`, `juz_number` `1..30`, dan `hizb_number` `1..60`.
- Selalu encode query string manual input: `q`, `tag`, `translation_source`, `recitation_id`, dan RAG question/body.

Response shape ringkas:

| Endpoint | Response shape |
| --- | --- |
| `GET /v1/categories` | `Category[]` array langsung |
| `GET /v1/quran/surahs` | `QuranSurah[]` array langsung |
| `GET /v1/quran/recitations` | `QuranRecitation[]` array langsung |
| `GET /v1/quran/translation-sources` | `QuranTranslationSource[]` array langsung |
| `GET /v1/quran/juz`, `/hizbs` | `QuranNavigationSegment[]` array langsung |
| `GET /v1/quran/.../ayahs` | `QuranAyah[]` array langsung |
| `GET /v1/books/{book_id}/toc` | `BookTOCNode[]` array langsung |
| `GET /v1/books/{book_id}/headings` | `BookHeading[]` array langsung |
| `GET /v1/authors` | `{ "authors": Author[], "total": number }` |
| `GET /v1/books` | `{ "books": Book[], "total": number, "stats": BookCatalogStats }` |
| `GET /v1/books/{book_id}/pages` | `{ "pages": BookPage[], "total": number }` |
| `GET /v1/quran/search` | `{ "results": QuranSearchResult[], "total": number }` |
| `GET /v1/books/{book_id}/quran-references` | `{ "references": BookQuranReference[], "total": number }` |
| `GET /v1/me/saved-items` | `{ "items": SavedItem[], "total": number }` |
| `GET /v1/me/saved-items/tags` | `{ "tags": string[] }` |
| `GET /v1/me/quran/progress/surahs` | `{ "surahs": QuranReadingProgress[] }` |

## 2. Error Contract

Semua error REST memakai shape:

```json
{
  "error": "invalid request body",
  "code": "invalid_request_body",
  "message": "invalid request body",
  "request_id": "..."
}
```

`error` tetap ada untuk client lama. FE baru sebaiknya simpan `request_id` untuk debugging dan boleh branch ringan memakai `code`, tetapi status HTTP tetap sumber utama.

Mobile sebaiknya branch berdasarkan HTTP status terlebih dahulu:

| Status | Arti umum | Mobile behavior |
| --- | --- | --- |
| `400` | Request invalid, query salah, body salah, language unsupported | Jangan retry otomatis. Highlight field atau reset pilihan invalid. |
| `401` | Token hilang, invalid, atau expired | Hapus token lokal dan arahkan ke login. |
| `403` | Role tidak cukup atau login email belum verified | Untuk login, tampilkan screen verifikasi email. Untuk protected role, tampilkan akses terbatas. |
| `404` | Resource tidak ada | Tampilkan empty/not found state. |
| `409` | Conflict, misalnya email sudah terdaftar | Tampilkan pesan actionable. |
| `412` | `If-Match` stale pada editorial mutation | Refresh resource lalu minta user retry/merge. |
| `429` | Rate limit | Tampilkan cooldown dan retry setelah jeda. |
| `500` | Server error | Retry manual, logging client. |
| `503` | Service dependency bermasalah, misalnya email delivery | Tampilkan pesan retry. |

Minimal client wrapper:

```ts
export class ApiError extends Error {
  status: number;
  code?: string;

  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

export async function apiFetch<T>(
  baseURL: string,
  path: string,
  options: RequestInit & { token?: string } = {},
): Promise<T> {
  const headers = new Headers(options.headers);
  headers.set("Accept", "application/json");
  if (options.body && !headers.has("Content-Type")) headers.set("Content-Type", "application/json");
  if (options.token) headers.set("Authorization", `Bearer ${options.token}`);

  const response = await fetch(`${baseURL}${path}`, { ...options, headers });
  const text = await response.text();
  const data = text ? JSON.parse(text) : null;

  if (!response.ok) {
    const error = new ApiError(response.status, data?.message || data?.error || "request failed");
    error.code = data?.code;
    throw error;
  }

  return data as T;
}
```

## 2.1 Public Cache Contract

Public `GET` reader/Quran endpoints can return:

```http
Cache-Control: public, max-age=300, stale-while-revalidate=86400
ETag: W/"..."
Last-Modified: ...
X-Request-ID: ...
```

Mobile may use normal HTTP cache or send `If-None-Match` on repeat fetches. A `304 Not Modified` response means reuse the local cached JSON for that URL.

## 3. Language Contract

Supported content languages:

```ts
export type ContentLang = "ar" | "id" | "en";
```

Rules:

- FE mobile harus selalu mengirim `?lang={selectedLang}` untuk Quran dan kitab.
- Empty backend `lang` default ke `id`, tetapi mobile jangan bergantung pada default itu.
- Region tag dinormalisasi: `en-US -> en`, `id-ID -> id`, `ar-SA -> ar`.
- Unsupported explicit language menghasilkan `400 {"error":"unsupported language"}`.
- Tidak ada fallback otomatis `en -> id` untuk konten terjemahan.
- Arabic/source tetap menjadi fallback baca yang canonical.

Resolver rekomendasi:

```ts
export function normalizeContentLang(input?: string | null): ContentLang {
  const primary = (input || "id").trim().toLowerCase().replace("_", "-").split("-")[0];
  return primary === "ar" || primary === "id" || primary === "en" ? primary : "id";
}

export function resolveContentLang(input: {
  explicitLang?: string | null;
  profileLang?: string | null;
  localLang?: string | null;
  deviceLang?: string | null;
}): ContentLang {
  return normalizeContentLang(
    input.explicitLang || input.profileLang || input.localLang || input.deviceLang || "id",
  );
}
```

## 4. Auth Flow

Mobile auth memakai JWT Bearer token. Tidak ada refresh token, cookie auth, session DB, MFA, atau logout server-side.

Endpoint ringkas:

| Method | Path | Auth | Success |
| --- | --- | --- | --- |
| `POST` | `/v1/auth/register` | Public | `201 User` |
| `POST` | `/v1/auth/login` | Public | `200 {"token":"..."}` |
| `POST` | `/v1/auth/verify-email` | Public | `200 {"email_verified":true}` |
| `POST` | `/v1/auth/resend-verification` | Public | `202 {"accepted":true}` |
| `POST` | `/v1/auth/forgot-password` | Public | `202 {"accepted":true}` |
| `POST` | `/v1/auth/reset-password` | Public | `200 {"password_reset":true}` |
| `POST` | `/v1/auth/change-password` | Bearer | `200 {"password_changed":true}` |
| `POST` | `/v1/auth/change-email/request` | Bearer | `202 {"accepted":true}` |
| `POST` | `/v1/auth/change-email/verify` | Bearer | `200 {"email_changed":true}` |
| `POST` | `/v1/auth/delete-account` | Bearer | `200 {"account_deleted":true}` |

Auth gotchas:

- Register sukses membuat user `email_verified=false`; user baru belum bisa login sebelum verify email.
- Login user yang belum verified mengembalikan `403 {"error":"email not verified"}`. Mobile harus tampilkan screen "cek email" dan tombol resend.
- `POST /v1/auth/resend-verification` dan `POST /v1/auth/forgot-password` sengaja memakai response `202 {"accepted":true}`.
- `POST /v1/auth/change-email/verify` tetap butuh Bearer token user yang sedang login. Jika deep link membuka app dari cold start, restore session dulu sebelum submit token.
- Backend tidak punya refresh token. Saat protected endpoint mengembalikan `401`, hapus token lokal dan minta login ulang.
- Setelah reset password, change password, change email, atau delete account, JWT lama akan invalid.
- Jangan trim password. Password valid `8..72` bytes, bukan karakter.
- FE baru sebaiknya pakai `name` atau `display_name` saat register. `username` masih diterima untuk kompatibilitas client lama.

Register request:

```json
{
  "name": "Ahmad",
  "email": "ahmad@example.com",
  "password": "correct horse battery"
}
```

Login request:

```json
{
  "email": "ahmad@example.com",
  "password": "correct horse battery"
}
```

Login success:

```json
{
  "token": "eyJhbGciOiJIUzI1NiIs..."
}
```

Verify email request:

```json
{
  "token": "token-dari-deep-link"
}
```

Resend verification dan forgot password request:

```json
{
  "email": "ahmad@example.com"
}
```

Reset password request:

```json
{
  "token": "token-dari-deep-link",
  "password": "new correct horse battery"
}
```

Change password request:

```json
{
  "current_password": "old correct horse battery",
  "new_password": "new correct horse battery"
}
```

Change email request + verify:

```json
{
  "current_password": "correct horse battery",
  "new_email": "ahmad-baru@example.com"
}
```

```json
{
  "token": "token-dari-deep-link"
}
```

Delete account request:

```json
{
  "current_password": "correct horse battery"
}
```

Mobile startup session flow:

1. Ambil token dari secure storage.
2. Jika token ada, panggil `GET /v1/user/profile`.
3. Jika `200`, simpan `UserAccount`, pakai `preferences.preferred_content_lang`.
4. Jika `401`, hapus token dan tampilkan login.
5. Jika `onboarding_required=true`, arahkan ke onboarding sebelum reader utama.

Storage rekomendasi:

- iOS: Keychain.
- Android: EncryptedSharedPreferences/Keystore.
- Jangan simpan token di plain AsyncStorage tanpa enkripsi.
- Logout client cukup hapus token lokal dan state user.

## 5. Profile, Onboarding, Preferences

Endpoint:

| Method | Path | Auth | Kegunaan |
| --- | --- | --- | --- |
| `GET` | `/v1/user/profile` | Bearer | Bootstrap user saat app start/login. |
| `PATCH` | `/v1/user/profile` | Bearer | Update profile dasar. |
| `PATCH` | `/v1/user/onboarding` | Bearer | Complete first-run onboarding. |
| `PATCH` | `/v1/user/preferences` | Bearer | Update settings reader/Quran. |
| `GET` | `/v1/user/email-preferences` | Bearer | Ambil opt-in email. |
| `PATCH` | `/v1/user/email-preferences` | Bearer | Update opt-in email. |

Core type:

```ts
export type UserAccount = {
  id: string;
  username: string;
  email: string;
  role: "user" | "editor" | "admin" | string;
  email_verified: boolean;
  created_at: string;
  updated_at: string;
  profile: {
    user_id: string;
    display_name?: string | null;
    timezone?: string | null;
    country_code?: string | null;
    onboarding_version: number;
    onboarding_completed_at?: string | null;
    personalization_enabled: boolean;
    created_at: string;
    updated_at: string;
  };
  preferences: {
    user_id: string;
    preferred_ui_lang: ContentLang;
    preferred_content_lang: ContentLang;
    fallback_langs: ContentLang[];
    arabic_level: "none" | "basic" | "intermediate" | "advanced" | "native";
    reader_mode: "arabic_translation" | "translation_only" | "arabic_only";
    interests: string[];
    daily_goal_minutes?: number | null;
    quran_translation_source_id?: string | null;
    quran_recitation_id?: string | null;
    created_at: string;
    updated_at: string;
  };
  onboarding_required: boolean;
};
```

Onboarding request contoh:

```json
{
  "display_name": "Ahmad",
  "timezone": "Asia/Jakarta",
  "country_code": "ID",
  "personalization_enabled": true,
  "preferred_ui_lang": "id",
  "preferred_content_lang": "id",
  "fallback_langs": ["id", "en"],
  "arabic_level": "basic",
  "reader_mode": "arabic_translation",
  "interests": ["quran_daily", "tafsir", "hadith"],
  "daily_goal_minutes": 15,
  "quran_translation_source_id": "qul-kfgqpc-id-simple",
  "quran_recitation_id": "mishari-rashid-alafasy"
}
```

Accepted interests:

```ts
export type UserInterest =
  | "adab"
  | "aqidah"
  | "arabic_language"
  | "fiqh"
  | "hadith"
  | "learn_kitab"
  | "memorization"
  | "murottal"
  | "quran_daily"
  | "research"
  | "sirah"
  | "tafsir";
```

Profile update request fields are optional:

```json
{
  "display_name": "Ahmad",
  "timezone": "Asia/Jakarta",
  "country_code": "ID",
  "personalization_enabled": true
}
```

Preferences update request fields are optional and do not mark onboarding complete:

```json
{
  "preferred_ui_lang": "id",
  "preferred_content_lang": "en",
  "fallback_langs": ["en", "id"],
  "arabic_level": "intermediate",
  "reader_mode": "arabic_translation",
  "interests": ["research", "fiqh"],
  "daily_goal_minutes": 20,
  "quran_translation_source_id": "qul-kfgqpc-id-simple",
  "quran_recitation_id": "qul-ayah-recitation-mishari-rashid-al-afasy-murattal-hafs-953"
}
```

Email preferences:

```ts
export type EmailSubscription = {
  user_id: string;
  marketing_opt_in: boolean;
  opted_in_at?: string | null;
  opted_out_at?: string | null;
  source?: string;
  created_at: string;
  updated_at: string;
};
```

```json
{
  "marketing_opt_in": true
}
```

Deep link unsubscribe public bisa memanggil `GET /v1/email/unsubscribe?token={token}` atau `POST /v1/email/unsubscribe` dengan:

```json
{
  "token": "unsubscribe-token"
}
```

## 6. Quran Reader

Public Quran endpoints tidak butuh auth. Auth hanya dibutuhkan untuk progress, saved items, dan preferences.

Endpoint ringkas:

| Method | Path | Auth | Kegunaan |
| --- | --- | --- | --- |
| `GET` | `/v1/quran/surahs?lang=id` | Public | Surah index. |
| `GET` | `/v1/quran/surahs/{surah_id}?lang=id` | Public | Detail/header surah. |
| `GET` | `/v1/quran/surahs/{surah_id}/ayahs?lang=id&include_translation=true&include_audio=false&view=reader_minimal` | Public | Ayah list compact untuk reader. |
| `GET` | `/v1/quran/ayahs/{ayah_key}?lang=id&include_audio=true` | Public | Detail satu ayah. |
| `GET` | `/v1/quran/search?q=rahmat&lang=id&limit=20&offset=0` | Public | Search Quran. |
| `GET` | `/v1/quran/juz` | Public | List juz. |
| `GET` | `/v1/quran/juz/{juz_number}/ayahs?lang=id&view=reader_minimal` | Public | Reader by juz. |
| `GET` | `/v1/quran/hizbs` | Public | List hizb. |
| `GET` | `/v1/quran/hizbs/{hizb_number}/ayahs?lang=id&view=reader_minimal` | Public | Reader by hizb. |
| `GET` | `/v1/quran/recitations` | Public | Pilihan audio/reciter. |
| `GET` | `/v1/quran/translation-sources?lang=id` | Public | Pilihan sumber terjemahan. |

Quran query params penting:

| Param | Dipakai di | Default | Notes |
| --- | --- | --- | --- |
| `lang` | Semua Quran read/search/navigation | `id` | FE tetap wajib kirim dari state. |
| `include_info` | `/quran/surahs` | `false` | `true` memasukkan `info.text_html` untuk semua surah; lebih berat. |
| `from`, `to` | `/quran/surahs/{surah_id}/ayahs` | semua ayah | Positive ayah number untuk range lokal dalam surah. |
| `translation_source` | Ayah list, single ayah, juz/hizb ayahs | backend default per language | Kirim source ID dari `/quran/translation-sources`. |
| `include_translation` | Ayah list, juz/hizb ayahs | `true` | Single ayah endpoint tidak memakai param ini. |
| `include_audio` | Ayah list, single ayah, juz/hizb ayahs | `false` | `true` menambahkan `audio[]` jika tersedia. |
| `recitation_id` | Endpoint dengan `include_audio=true` | playable default | Unknown explicit ID menghasilkan `404 quran recitation not found`. |
| `view` | Ayah list, juz/hizb ayahs | `full` | Gunakan `reader_minimal` untuk payload reader compact; default tetap response lama. |
| `q` | `/quran/search` | required | Trim dan encode query. |

Recommended Quran screen bootstrap:

1. `GET /v1/quran/surahs?lang={lang}` untuk index.
2. `GET /v1/quran/translation-sources?lang={lang}` jika user bisa memilih sumber terjemahan.
3. `GET /v1/quran/recitations` jika audio enabled.
4. `GET /v1/quran/surahs/{surah_id}/ayahs?lang={lang}&translation_source={optional}&include_translation=true&include_audio={boolean}&recitation_id={optional}&view=reader_minimal` untuk reader.

Display rules:

- Selalu render Arabic Quran text dari `text_qpc_hafs`.
- Jika `text_qpc_hafs` kosong, pakai `text_imlaei_simple`.
- Render `translation.text` hanya jika `translation !== null`.
- Untuk ayah list reader baru, prefer `view=reader_minimal`; `translation` dan `audio` akan omitted ketika tidak diminta atau tidak tersedia.
- Untuk `lang=ar`, backend mengembalikan Arabic-only mode: translation `null`, dan translation tab sebaiknya disembunyikan.
- Jangan render Indonesian sebagai fallback English. Gunakan `available_translation_langs` untuk menawarkan switch bahasa.
- Search result response adalah `{results,total}`; setiap `result.ayah` tetap mengikuti exact requested-language display rules.
- Surah info HTML ada di `surah.info.text_html` dan hanya ada jika single-surah endpoint dipakai atau `include_info=true`.

Audio rules:

- Gunakan `public_url ?? audio_url` sebagai playable URL.
- Pada `view=reader_minimal`, gunakan langsung `audio[].url`.
- `public_url` lebih disukai karena app-owned CDN.
- `audio_url` tetap valid untuk local/dev fallback.
- `r2_key` adalah storage metadata, bukan URL browser/player.
- Segment timestamp dalam milliseconds.
- `audio[]` bisa kosong walaupun `include_audio=true`; jangan crash player.
- `QuranRecitation.is_default=true` bisa tidak ada jika belum ada recitation yang playable penuh.

Core Quran types yang perlu FE model:

```ts
export type QuranAyah = {
  surah_id: number;
  ayah_number: number;
  ayah_key: string;
  text_qpc_hafs?: string | null;
  text_imlaei_simple?: string | null;
  page_number?: number | null;
  juz_number?: number | null;
  hizb_number?: number | null;
  translation: QuranTranslation | null;
  audio?: QuranAudioTrack[];
  requested_lang: ContentLang;
  available_translation_langs: ContentLang[];
  translation_missing: boolean;
  availability: {
    translation: AvailabilityDecision;
    audio: AvailabilityDecision;
  };
  updated_at: string;
};

export type QuranTranslation = {
  source_id: string;
  lang: ContentLang;
  text: string;
  footnotes?: unknown;
  chunks?: unknown;
  metadata?: unknown;
  updated_at: string;
};

export type QuranAudioSegment = {
  segment_index: number;
  ayah_key: string;
  timestamp_from_ms: number;
  timestamp_to_ms: number;
  duration_ms?: number | null;
};

export type QuranAudioTrack = {
  recitation_id: string;
  track_type: "ayah" | "surah" | string;
  track_key: string;
  surah_id: number;
  ayah_number?: number | null;
  audio_url?: string | null;
  public_url?: string | null;
  r2_key?: string | null;
  duration_ms?: number | null;
  duration_seconds?: number | null;
  mime_type?: string | null;
  segments?: QuranAudioSegment[];
};

export type QuranTranslationSource = {
  id: string;
  lang: ContentLang;
  name: string;
  translator?: string | null;
  coverage: { translated_ayahs: number; total_ayahs: number; percent: number };
  is_default: boolean;
};

export type QuranRecitation = {
  id: string;
  name: string;
  reciter_name?: string | null;
  style?: string | null;
  mode: "ayah" | "surah" | string;
  track_count: number;
  public_track_count: number;
  playable_track_count: number;
  has_public_audio: boolean;
  has_playable_audio: boolean;
  is_default: boolean;
};
```

Progress Quran protected:

| Method | Path | Auth | Kegunaan |
| --- | --- | --- | --- |
| `GET` | `/v1/me/quran/progress` | Bearer | Resume terakhir lintas surah. |
| `PUT` | `/v1/me/quran/progress` | Bearer | Simpan posisi terakhir. |
| `GET` | `/v1/me/quran/progress/surahs` | Bearer | Semua progress per surah. |
| `GET` | `/v1/me/quran/progress/surahs/{surah_id}` | Bearer | Progress satu surah. |

`GET` progress Quran bisa mengembalikan `404` jika belum pernah ada progress. FE sebaiknya treat sebagai `null` dan mulai dari ayah pertama atau posisi lokal terakhir.

Save Quran progress request:

```json
{
  "ayah_key": "73:4",
  "client_observed_at": "2026-06-01T10:00:00Z"
}
```

Response:

```ts
export type QuranReadingProgress = {
  user_id: string;
  surah_id: number;
  ayah_number: number;
  ayah_key: string;
  position_percent: number;
  observed_at: string;
  updated_at: string;
};
```

Mobile save strategy:

- Save saat user berhenti scroll beberapa detik, pindah ayah aktif, pause audio, background app, atau keluar screen.
- Debounce 2-5 detik.
- Kirim `client_observed_at` agar event lama tidak menimpa progress baru.

## 7. Kitab Reader

Public kitab endpoints tidak butuh auth. Auth dibutuhkan untuk progress, saved items, dan feedback bisa tetap public sesuai endpoint feedback.

Endpoint ringkas:

| Method | Path | Auth | Kegunaan |
| --- | --- | --- | --- |
| `GET` | `/v1/categories?lang=id` | Public | Category list. |
| `GET` | `/v1/authors?lang=id` | Public | Author list. |
| `GET` | `/v1/books?lang=id&limit=20&offset=0` | Public | Catalog kitab. |
| `GET` | `/v1/books/{book_id}?lang=id` | Public | Detail kitab dan language coverage. |
| `GET` | `/v1/books/{book_id}/pages?limit=50&offset=0` | Public | Page list. |
| `GET` | `/v1/books/{book_id}/pages/{page_id}` | Public | Page detail. |
| `GET` | `/v1/books/{book_id}/headings` | Public | Flat headings. |
| `GET` | `/v1/books/{book_id}/sections/{heading_id}?lang=id` | Public | Section detail legacy. |
| `GET` | `/v1/books/{book_id}/toc?lang=id&include_audio=false` | Public | TOC tree. |
| `GET` | `/v1/books/{book_id}/toc/{heading_id}/read?lang=id&include_quran_references=false` | Public | Reader section body. |
| `GET` | `/v1/books/{book_id}/toc/{heading_id}/playlist?lang=id` | Public | Audio playlist for TOC section. |
| `GET` | `/v1/books/{book_id}/quran-references?lang=id&heading_id=10&status=approved` | Public | Quran references linked to kitab. |
| `POST` | `/v1/books/{book_id}/rag?lang=id` | Public, rate limited | Ask AI over book. |
| `POST` | `/v1/books/{book_id}/toc/{heading_id}/translation-feedback?lang=id` | Public, rate limited | Feedback terjemahan. |

Kitab query params penting:

| Param | Dipakai di | Default | Notes |
| --- | --- | --- | --- |
| `lang` | Catalog, book detail, TOC, read, playlist, references, RAG, feedback | `id` | FE tetap wajib kirim dari state. |
| `q` | `/authors`, `/books`, `/headings` | empty | Search text, encode query. |
| `category_id`, `author_id` | `/books` | none | Positive int filter. |
| `has_content` | `/books` | none | Boolean filter; kirim `true` untuk reader-ready catalog. |
| `limit`, `offset` | `/authors`, `/books`, `/pages`, references | default `50`, `0` | Invalid `limit/offset` jatuh ke default pada sebagian endpoint. |
| `include_audio` | `/books/{book_id}/toc` | `false` | `true` memasukkan metadata audio di node TOC jika tersedia. |
| `include_quran_references` | `/books/{book_id}/toc/{heading_id}/read` | `false` | `true` embeds approved Quran refs scoped to the current heading. |
| `heading_id` | `/books/{book_id}/quran-references` | none | Positive int filter. Use this instead of client-side filtering for section refs. |
| `status` | `/books/{book_id}/quran-references` | `approved` | Allowed: `approved`, `pending`, `rejected`, `ambiguous`, `needs_review`, `all`. Mobile reader umumnya pakai `approved`. |

Recommended kitab reader flow:

1. `GET /v1/books/{book_id}?lang={lang}` untuk header, metadata, dan `language_coverage`.
2. `GET /v1/books/{book_id}/toc?lang={lang}&include_audio={boolean}` untuk navigasi.
3. `GET /v1/books/{book_id}/toc/{heading_id}/read?lang={lang}&include_quran_references=true` untuk body section dan referensi Quran section.
4. Alternatif jika perlu lazy load: `GET /v1/books/{book_id}/quran-references?lang={lang}&heading_id={headingId}&status=approved`.
5. `GET /v1/books/{book_id}/toc/{heading_id}/playlist?lang={lang}` jika audio kitab aktif.

Display rules:

- Selalu simpan kemampuan render `original_html` atau Arabic/source content.
- `original_html` dan editorial `content_html` yang dikembalikan backend sudah melewati sanitizer allowlist server-side; FE tetap harus render hanya di area reader/editorial preview, bukan sebagai HTML arbitrer dari user.
- Render `translation.content` hanya jika `translation !== null`.
- Untuk `lang=ar`, render Arabic/source sebagai utama dan sembunyikan feedback terjemahan.
- Tampilkan feedback hanya jika `translation !== null && translation.lang === selectedLang`.
- Gunakan nested `availability` untuk menentukan tab, badge, empty state, dan offer switch bahasa.
- `GET /v1/books/{book_id}` detail menyertakan `language_coverage`; list `/v1/books` tidak.
- `GET /v1/books/{book_id}/toc/{heading_id}/read` sudah punya `breadcrumb`, `children`, `previous`, dan `next`; mobile reader tidak perlu hit TOC ulang hanya untuk next/previous.
- `BookTOCPlaylist.missing_count > 0` berarti sebagian subtree belum punya audio exact-language.

Core kitab read types yang perlu FE model:

```ts
export type BookTOCNode = {
  book_id: number;
  heading_id: number;
  parent_id?: number | null;
  page_id: number;
  depth: number;
  ordinal: number;
  title: string;
  requested_lang: ContentLang;
  title_lang: ContentLang;
  is_title_fallback: boolean;
  summary?: string | null;
  summary_lang?: ContentLang | null;
  has_summary: boolean;
  has_audio: boolean;
  has_translation: boolean;
  translation_missing: boolean;
  available_translation_langs: ContentLang[];
  available_summary_langs: ContentLang[];
  audio?: SectionAudio | null;
  availability: {
    title: AvailabilityDecision;
    translation: AvailabilityDecision;
    summary: AvailabilityDecision;
    audio: AvailabilityDecision;
  };
  children: BookTOCNode[];
};

export type BookTOCLink = Omit<BookTOCNode, "children" | "audio">;

export type BookTOCRead = {
  book_id: number;
  heading_id: number;
  title: string;
  requested_lang: ContentLang;
  title_lang: ContentLang;
  is_title_fallback: boolean;
  summary?: string | null;
  summary_lang?: ContentLang | null;
  has_summary: boolean;
  translation_missing: boolean;
  available_translation_langs: ContentLang[];
  breadcrumb: BookTOCLink[];
  children: BookTOCLink[];
  previous?: BookTOCLink | null;
  next?: BookTOCLink | null;
  start_page_id: number;
  end_page_id: number;
  original_html: string;
  original_text: string;
  translation: SectionTranslation | null;
  audio: SectionAudio | null;
  availability: BookTOCNode["availability"];
};

export type SectionTranslation = {
  book_id: number;
  heading_id: number;
  lang: ContentLang;
  title?: string | null;
  content: string;
  translation_status: string;
  updated_at: string;
};

export type SectionAudio = {
  book_id: number;
  heading_id: number;
  lang: ContentLang;
  url: string;
  narrator?: string | null;
  duration_seconds?: number | null;
  mime_type?: string | null;
  updated_at: string;
};
```

RAG request:

```json
{
  "question": "Apa definisi hadis sahih?",
  "stream": false,
  "include_trace": false,
  "max_citations": 5
}
```

RAG non-stream response:

```ts
export type BookRAGResponse = {
  book_id: number;
  requested_lang: ContentLang;
  question: string;
  answer: string;
  citations: Array<{
    ref: string;
    book_id: number;
    heading_id: number;
    heading_title: string;
    page_id: number;
    printed_page?: string | null;
    part?: string | null;
    anchor: string;
    quote: string;
    url: string;
  }>;
  trace?: unknown | null;
};
```

Jika `stream=true`, response memakai Server-Sent Events dengan headers `Content-Type: text/event-stream`. Event yang perlu ditangani:

| Event | Payload | FE behavior |
| --- | --- | --- |
| `meta` | `{book_id, question}` | Init state. |
| `delta` | `{text}` | Append answer chunk. |
| `citations` | `BookRAGCitation[]` | Render source cards. |
| `done` | `BookRAGResponse` | Finalize answer. |
| `error` | `{"error":"..."}` | Stop stream and show error. |

Translation feedback request:

```json
{
  "vote": "dislike",
  "reason": "inaccurate",
  "note": "Terjemahan bagian ini kurang tepat.",
  "client_id": "mobile-install-or-session-id"
}
```

Allowed feedback reason:

```ts
type TranslationFeedbackReason =
  | "inaccurate"
  | "unclear"
  | "style"
  | "typo"
  | "formatting"
  | "other";
```

## 8. Availability Contract

Backend memberi `availability` supaya mobile tidak menebak-nebak dari nullable field.

```ts
export type AvailabilityAction =
  | "show_requested"
  | "show_arabic"
  | "offer_available_lang"
  | "hide_translation_tab"
  | "hide_audio";

export type AvailabilityDecision = {
  action: AvailabilityAction;
  reason:
    | "source_language"
    | "exact_available"
    | "arabic_fallback"
    | "alternative_langs_available"
    | "unavailable";
  requested_lang: ContentLang;
  display_lang: ContentLang;
  is_fallback: boolean;
  missing: boolean;
  available_langs: ContentLang[];
};
```

UI mapping:

| Action | Mobile behavior |
| --- | --- |
| `show_requested` | Render asset sesuai bahasa yang diminta. |
| `show_arabic` | Render Arabic/source dengan label jika perlu. |
| `offer_available_lang` | Tampilkan source dan tawarkan switch ke `available_langs`. |
| `hide_translation_tab` | Sembunyikan atau disable tab terjemahan. |
| `hide_audio` | Sembunyikan atau disable audio player. |

## 9. Reading Progress Kitab

Protected endpoint:

| Method | Path | Auth | Kegunaan |
| --- | --- | --- | --- |
| `GET` | `/v1/me/progress/{book_id}` | Bearer | Ambil progress kitab. |
| `PUT` | `/v1/me/progress/{book_id}` | Bearer | Simpan progress by page/heading. |
| `PUT` | `/v1/me/progress/{book_id}/toc/{heading_id}` | Bearer | Simpan progress TOC section. |

`GET /v1/me/progress/{book_id}` bisa `404 {"error":"progress not found"}` jika user belum pernah membaca kitab itu. FE sebaiknya treat sebagai `null` dan mulai dari TOC pertama atau posisi lokal terakhir.

Save progress request:

```json
{
  "page_id": 12,
  "heading_id": 10,
  "progress_percent": 32.5
}
```

Save TOC progress request:

```json
{
  "progress_percent": 32.5
}
```

Response:

```ts
export type ReadingProgress = {
  user_id: string;
  book_id: number;
  page_id?: number | null;
  heading_id?: number | null;
  progress_percent?: number | null;
  updated_at: string;
};
```

Mobile save strategy:

- Save saat heading aktif berubah, scroll idle, app background, atau keluar reader.
- Debounce 2-5 detik.
- Jangan spam setiap scroll pixel.
- Jika user guest, simpan progress lokal dan sync setelah login jika produk menginginkan.

## 10. Saved Items / Bookmarks

Protected endpoint:

| Method | Path | Auth | Kegunaan |
| --- | --- | --- | --- |
| `GET` | `/v1/me/saved-items?item_type=&book_id=&surah_id=&tag=&limit=50&offset=0` | Bearer | List bookmarks/notes. |
| `POST` | `/v1/me/saved-items` | Bearer | Save atau update target yang sama. |
| `GET` | `/v1/me/saved-items/tags` | Bearer | Tag autocomplete/filter. |
| `PATCH` | `/v1/me/saved-items/{id}` | Bearer | Update label, note, tags. |
| `DELETE` | `/v1/me/saved-items/{id}` | Bearer | Delete bookmark. |

Saved item rules:

- `book_page` wajib `book_id + page_id`; tidak boleh punya Quran target.
- `book_heading` wajib `book_id + heading_id`; tidak boleh punya `page_id` atau Quran target.
- `quran_ayah` wajib `ayah_key`; backend mengisi/menormalisasi `surah_id` dari `ayah_key`.
- `quran_range` wajib `surah_id + from_ayah_number + to_ayah_number`.
- Jika `quran_range` punya `from_ayah_number === to_ayah_number`, backend menormalisasi menjadi `quran_ayah`.
- Tags di-trim, lowercase, dedupe, max `20` tags, max `64` char per tag.
- `label` max `255`, `note` max `2000`.
- `POST /v1/me/saved-items` bersifat upsert target yang sama; gunakan response sebagai source of truth.
- `DELETE` sukses mengembalikan `204` tanpa body.

Types:

```ts
export type SavedItemType =
  | "book_page"
  | "book_heading"
  | "quran_ayah"
  | "quran_range";

export type SavedItem = {
  id: string;
  user_id: string;
  item_type: SavedItemType;
  book_id?: number | null;
  page_id?: number | null;
  heading_id?: number | null;
  surah_id?: number | null;
  ayah_key?: string | null;
  from_ayah_number?: number | null;
  to_ayah_number?: number | null;
  label?: string | null;
  note?: string | null;
  tags: string[];
  created_at: string;
  updated_at: string;
};
```

Save Quran ayah:

```json
{
  "item_type": "quran_ayah",
  "surah_id": 73,
  "ayah_key": "73:4",
  "label": "Murottal latihan",
  "note": "Ayat untuk diulang",
  "tags": ["hafalan"]
}
```

Save Quran range:

```json
{
  "item_type": "quran_range",
  "surah_id": 73,
  "from_ayah_number": 1,
  "to_ayah_number": 6,
  "label": "Awal Al-Muzzammil",
  "tags": ["qiyam"]
}
```

Save kitab heading:

```json
{
  "item_type": "book_heading",
  "book_id": 797,
  "heading_id": 10,
  "label": "Definisi sahih",
  "tags": ["hadith"]
}
```

Saved item response bersifat reference-only. Mobile perlu hydrate target content sendiri dengan endpoint Quran/kitab sesuai `item_type`.

## 11. Mobile Screen Blueprint

### App Launch

1. Load local settings: selected language, theme, cached profile.
2. Load token dari secure storage.
3. Jika token ada: `GET /v1/user/profile`.
4. Preload Quran surah index: `GET /v1/quran/surahs?lang={lang}`.
5. Preload kitab categories/books seperlunya.

### Login

1. `POST /v1/auth/login`.
2. Simpan token di secure storage.
3. `GET /v1/user/profile`.
4. Jika onboarding required, buka onboarding.
5. Jika tidak, buka home.

### Home

Recommended data:

- `GET /v1/quran/surahs?lang={lang}` untuk Quran entry.
- `GET /v1/books?lang={lang}&limit=20&offset=0` untuk kitab list.
- Jika logged-in: `GET /v1/me/quran/progress` dan recent saved items.

### Quran Surah Reader

1. Ambil selected lang.
2. Ambil user recitation preference dari profile.
3. `GET /v1/quran/surahs/{surah_id}/ayahs?lang={lang}&translation_source={source_id}&include_translation=true&include_audio=true&recitation_id={recitation_id}&view=reader_minimal`.
4. Jika logged-in, `GET /v1/me/quran/progress/surahs/{surah_id}`; jika `404`, mulai dari ayah pertama.
5. Render Arabic, translation, audio.
6. Debounced `PUT /v1/me/quran/progress`.

### Kitab Reader

1. `GET /v1/books/{book_id}?lang={lang}`.
2. `GET /v1/books/{book_id}/toc?lang={lang}&include_audio={boolean}`.
3. Jika logged-in, `GET /v1/me/progress/{book_id}`; jika `404`, mulai dari TOC pertama.
4. `GET /v1/books/{book_id}/toc/{heading_id}/read?lang={lang}`.
5. Optional: playlist, Quran references, RAG.
6. Debounced `PUT /v1/me/progress/{book_id}/toc/{heading_id}`.

### Library / Catalog

1. `GET /v1/categories?lang={lang}`.
2. `GET /v1/authors?lang={lang}` jika filter author ada.
3. `GET /v1/books?lang={lang}&limit={limit}&offset={offset}`.
4. Infinite scroll dengan `total`.

### Saved Items

1. `GET /v1/me/saved-items?limit=50&offset=0`.
2. Untuk tiap item, route berdasarkan `item_type`.
3. Hydrate detail ketika item dibuka, bukan semua sekaligus.

## 12. Caching and Offline Strategy

Data yang aman dicache lebih lama:

| Data | TTL rekomendasi | Notes |
| --- | --- | --- |
| Quran surah list | 7-30 hari | Jarang berubah. |
| Quran ayahs Arabic | 30 hari atau persistent | Canonical content. |
| Quran translation | 7-30 hari | Key cache harus menyertakan `lang` dan translation source jika dipakai. |
| Quran recitations | 7 hari | Bisa berubah saat audio ingestion. |
| Kitab categories/authors | 1-7 hari | Key cache menyertakan `lang`. |
| Kitab book detail/TOC | 1-7 hari | Bisa berubah saat editorial publish. |
| Kitab section read | 1-7 hari | Key cache menyertakan `book_id`, `heading_id`, `lang`. |
| User profile/preferences | Session cache | Refresh saat app start dan setelah update settings. |
| Progress/saved items | Network first | Private data harus paling baru. |

Cache key wajib menyertakan:

- API version/base URL.
- Path.
- Query penting: `lang`, pagination, `include_audio`, `recitation_id`, translation source.
- User ID untuk private data.

Offline write queue:

- Aman untuk queue: progress save, saved item create/update/delete, preference update.
- Setiap queued write perlu timestamp lokal dan retry policy.
- Untuk progress Quran, selalu kirim `client_observed_at`.
- Untuk delete saved item, jangan re-create item lama dari stale queue.

## 13. Pagination and Lists

List endpoint di backend ini tidak memakai satu bentuk seragam. Cek response shape di section `1.1`.

Paginated private/admin-like list sering memakai:

```json
{
  "items": [],
  "total": 42
}
```

Reader public list sering memakai domain-specific key:

```json
{
  "books": [],
  "total": 42
}
```

Sebagian lightweight list mengembalikan array langsung:

```json
[
  {
    "surah_id": 1,
    "ayah_count": 7
  }
]
```

Mobile convention:

- Default `limit=20` untuk catalog UI.
- `limit=50` untuk saved items dan lightweight lists.
- Stop infinite scroll saat `offset + currentPageCount >= total`.
- Jangan asumsi urutan stabil tanpa query/filter yang sama.

## 14. Security Notes for Mobile

- Jangan log token, password, reset token, verification token, atau Authorization header.
- Jangan trim password. Spasi adalah bagian password.
- Password valid: 8 sampai 72 bytes.
- Deep link verification/reset/change-email/unsubscribe harus membaca `token` dari query param lalu mengirim ke backend.
- Setelah change password, reset password, change email, atau delete account, backend menginvalidasi JWT lama. Mobile harus clear token dan meminta login ulang bila terkena `401`.
- Untuk public feedback/RAG yang rate limited, siapkan UI cooldown pada `429`.

## 15. Suggested Mobile Modules

Struktur FE mobile yang disarankan:

```text
src/api/client.ts
src/api/auth.ts
src/api/profile.ts
src/api/quran.ts
src/api/kitab.ts
src/api/personal.ts
src/domain/language.ts
src/domain/availability.ts
src/domain/audio.ts
src/storage/secureToken.ts
src/storage/cacheKeys.ts
```

API functions minimal:

```ts
const apiBaseURL = Config.API_BASE_URL;

export const AuthAPI = {
  register: (body) => apiFetch(apiBaseURL, "/v1/auth/register", { method: "POST", body: JSON.stringify(body) }),
  login: (body) =>
    apiFetch<{ token: string }>(apiBaseURL, "/v1/auth/login", {
      method: "POST",
      body: JSON.stringify(body),
    }),
  verifyEmail: (token: string) =>
    apiFetch(apiBaseURL, "/v1/auth/verify-email", {
      method: "POST",
      body: JSON.stringify({ token }),
    }),
};

export const QuranAPI = {
  surahs: (lang: ContentLang) => apiFetch(apiBaseURL, `/v1/quran/surahs?lang=${lang}`),
  surahAyahs: (input: {
    surahID: number;
    lang: ContentLang;
    translationSource?: string | null;
    recitationID?: string | null;
  }) => {
    const params = new URLSearchParams({
      lang: input.lang,
      include_translation: "true",
      include_audio: "true",
    });
    if (input.translationSource) params.set("translation_source", input.translationSource);
    if (input.recitationID) params.set("recitation_id", input.recitationID);

    return apiFetch(apiBaseURL, `/v1/quran/surahs/${input.surahID}/ayahs?${params}`);
  },
};
```

Sesuaikan signature wrapper dengan framework mobile yang dipakai.

## 16. QA Checklist for FE Mobile

Auth:

- Register sukses menampilkan email verification state.
- Login user unverified gagal sesuai copy backend.
- Token invalid menghasilkan clear session.
- Forgot/reset password via deep link berhasil.

Language:

- `id`, `en`, `ar` semua mengirim query `lang`.
- `en-US` dinormalisasi ke `en`.
- Missing English translation tidak menampilkan Indonesian tanpa user switch.
- `lang=ar` menyembunyikan translation tab.

Quran:

- Arabic text selalu tampil.
- Translation null menghasilkan empty state yang benar.
- Audio memakai `public_url ?? audio_url`.
- Progress tersimpan saat app background.
- Search result membuka ayah yang tepat.

Kitab:

- Catalog fallback Arabic diberi label.
- TOC dan section read mengikuti `availability`.
- Translation feedback hanya muncul untuk exact translation.
- Progress heading tersimpan dengan debounce.
- Quran references membuka Quran reader.

Saved items:

- Save ayah, range, kitab heading.
- Duplicate target melakukan update, bukan duplicate card.
- Filter by tag/type berjalan.
- Delete optimistic rollback jika request gagal.

Offline/cache:

- Quran/kitab cached tetap bisa dibuka read-only.
- Private write queue retry setelah online.
- Cache per language tidak bercampur.

## 17. Endpoint Priority for MVP

MVP mobile paling kecil:

1. `POST /v1/auth/login`
2. `GET /v1/user/profile`
3. `PATCH /v1/user/onboarding`
4. `GET /v1/quran/surahs`
5. `GET /v1/quran/surahs/{surah_id}/ayahs?view=reader_minimal`
6. `GET /v1/books`
7. `GET /v1/books/{book_id}`
8. `GET /v1/books/{book_id}/toc`
9. `GET /v1/books/{book_id}/toc/{heading_id}/read`
10. `PUT /v1/me/quran/progress`
11. `PUT /v1/me/progress/{book_id}/toc/{heading_id}`
12. `POST /v1/me/saved-items`

Setelah MVP:

- Register + verify email.
- Forgot/reset password.
- Quran search.
- Audio recitation selection.
- Translation source selection.
- Saved item list/tags.
- Kitab RAG.
- Translation feedback.
